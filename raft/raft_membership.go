package raft

import (
	"fmt"
	"log"
	"net/rpc"
	"sync"
	"time"
)

type JoinRequest struct {
	NodeID   int
	NodeAddr string
}

type JoinResponse struct {
	Success       bool
	CurrentTerm   int
	CurrentLeader int
	Nodes         []NodeConfig
	ErrorMsg      string
}

// JoinClusterRPC allows a new node to request joining the cluster
func (rn *RaftNode) JoinClusterRPC(req JoinRequest, resp *JoinResponse) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	// Only the leader can add new nodes
	if rn.state != LEADER {
		resp.Success = false
		resp.CurrentTerm = rn.currentTerm
		resp.CurrentLeader = rn.leaderID
		resp.Nodes = rn.cluster.Nodes
		resp.ErrorMsg = "Not the leader, try contacting the leader directly"
		return nil
	}

	for _, node := range rn.cluster.Nodes {
		if node.ID == req.NodeID {
			if node.Address != req.NodeAddr {
				log.Printf("info: updating node %d address from %s to %s",
					req.NodeID, node.Address, req.NodeAddr)

				rn.updateNodeAddress(req.NodeID, req.NodeAddr)

				resp.Success = true
				resp.CurrentTerm = rn.currentTerm
				resp.CurrentLeader = rn.id
				resp.Nodes = rn.cluster.Nodes
				return nil
			}

			resp.Success = true
			resp.CurrentTerm = rn.currentTerm
			resp.CurrentLeader = rn.id
			resp.Nodes = rn.cluster.Nodes
			resp.ErrorMsg = "Node already in cluster"
			return nil
		}
	}

	newNode := NodeConfig{
		ID:      req.NodeID,
		Address: req.NodeAddr,
	}

	log.Printf("info: adding new node %d with address %s to cluster", req.NodeID, req.NodeAddr)

	rn.addNode(newNode)

	client, err := rn.connectToPeer(newNode)
	if err != nil {
		log.Printf("warn: added node %d but failed to establish connection: %v",
			newNode.ID, err)
	} else {
		rn.peerClients[newNode.ID] = client
	}

	success := rn.propagateConfigChange()

	if !success {
		log.Printf("error: failed to propagate configuration change to all followers")
	} else {
		log.Printf("info: successfully propagated configuration change to all followers")
	}

	resp.Success = true
	resp.CurrentTerm = rn.currentTerm
	resp.CurrentLeader = rn.id
	resp.Nodes = rn.cluster.Nodes
	return nil
}

type ConfigUpdateRequest struct {
	Term   int
	Leader int
	Nodes  []NodeConfig
}

type ConfigUpdateResponse struct {
	Success bool
	Term    int
}

// UpdateConfigurationRPC updates a node's cluster configuration
func (rn *RaftNode) UpdateConfigurationRPC(req ConfigUpdateRequest, resp *ConfigUpdateResponse) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	log.Printf("info: received configuration update with %d nodes from node %d", len(req.Nodes), req.Leader)

	if req.Term < rn.currentTerm {
		log.Printf("warn: rejecting configuration update from term %d (current term: %d)", req.Term, rn.currentTerm)
		resp.Success = false
		resp.Term = rn.currentTerm
		return nil
	}

	if req.Term > rn.currentTerm {
		log.Printf("info: updating term from %d to %d from configuration update", rn.currentTerm, req.Term)
		rn.currentTerm = req.Term
		rn.state = FOLLOWER
		rn.votedFor = -1
		rn.leaderID = req.Leader
	}

	rn.cluster.Nodes = req.Nodes
	rn.cluster.ClusterSize = len(req.Nodes)

	rn.rebuildPeersList()

	log.Printf("info: establishing connections to all peers after configuration update")
	rn.connectToAllPeers()

	resp.Success = true
	resp.Term = rn.currentTerm
	return nil
}

// addNode adds a new node to the cluster configuration
func (rn *RaftNode) addNode(node NodeConfig) {
	rn.cluster.Nodes = append(rn.cluster.Nodes, node)
	rn.cluster.ClusterSize = len(rn.cluster.Nodes)

	if node.ID != rn.id {
		rn.peers = append(rn.peers, node)
	}
}

// updateNodeAddress updates an existing node's address
func (rn *RaftNode) updateNodeAddress(nodeID int, newAddr string) {
	for i := range rn.cluster.Nodes {
		if rn.cluster.Nodes[i].ID == nodeID {
			rn.cluster.Nodes[i].Address = newAddr
			break
		}
	}

	for i := range rn.peers {
		if rn.peers[i].ID == nodeID {
			rn.peers[i].Address = newAddr

			if client, exists := rn.peerClients[nodeID]; exists {
				client.Close()
				delete(rn.peerClients, nodeID)
			}

			client, err := rn.connectToPeer(rn.peers[i])
			if err == nil {
				rn.peerClients[nodeID] = client
			} else {
				log.Printf("warn: failed to connect to updated peer %d: %v", nodeID, err)
			}
			break
		}
	}
}

// rebuildPeersList rebuilds the peers list from cluster nodes
func (rn *RaftNode) rebuildPeersList() {
	var newPeers []NodeConfig
	for _, node := range rn.cluster.Nodes {
		if node.ID != rn.id {
			newPeers = append(newPeers, node)
		}
	}
	rn.peers = newPeers
	log.Printf("info: rebuilt peers list, now have %d peers", len(rn.peers))
}

// connectToNewPeers attempts to connect to any peers not already connected
func (rn *RaftNode) connectToNewPeers() {
	for _, peer := range rn.peers {
		if _, exists := rn.peerClients[peer.ID]; !exists {
			log.Printf("info: connecting to new peer %d at %s", peer.ID, peer.Address)
			client, err := rn.connectToPeer(peer)
			if err == nil {
				rn.peerClients[peer.ID] = client
				log.Printf("info: successfully connected to peer %d", peer.ID)
			} else {
				log.Printf("warn: failed to connect to peer %d: %v", peer.ID, err)
			}
		}
	}
}

// connectToAllPeers attempts to connect to all peers, closing and reopening existing connections
func (rn *RaftNode) connectToAllPeers() {
	for id, client := range rn.peerClients {
		client.Close()
		delete(rn.peerClients, id)
	}

	for _, peer := range rn.peers {
		log.Printf("info: connecting to peer %d at %s", peer.ID, peer.Address)

		var client *rpc.Client
		var err error

		// Try up to 3 times with a short backoff
		for attempt := 0; attempt < 3; attempt++ {
			client, err = rn.connectToPeer(peer)
			if err == nil {
				break
			}
			log.Printf("warn: attempt %d failed to connect to peer %d: %v", attempt+1, peer.ID, err)
			time.Sleep(100 * time.Millisecond)
		}

		if err == nil {
			rn.peerClients[peer.ID] = client
			log.Printf("info: successfully connected to peer %d", peer.ID)
		} else {
			log.Printf("error: all attempts to connect to peer %d failed", peer.ID)
		}
	}
}

// connectToPeer establishes an RPC connection to a peer
func (rn *RaftNode) connectToPeer(peer NodeConfig) (*rpc.Client, error) {
	if !verifyNodeConnectivity(peer.Address) {
		return nil, fmt.Errorf("node at %s is not reachable", peer.Address)
	}

	client, err := rpc.Dial("tcp", peer.Address)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// propagateConfigChange sends the updated configuration to all peers, Returns true if all updates were successful
func (rn *RaftNode) propagateConfigChange() bool {
	if rn.state != LEADER {
		return false
	}

	var wg sync.WaitGroup
	allSuccessful := true
	var mu sync.Mutex

	for _, peer := range rn.peers {
		wg.Add(1)
		go func(peerID int, peerAddr string) {
			defer wg.Done()

			client, exists := rn.peerClients[peerID]
			if !exists {
				log.Printf("warn: no connection to peer %d for config update", peerID)

				var err error
				client, err = rn.connectToPeer(NodeConfig{ID: peerID, Address: peerAddr})
				if err != nil {
					log.Printf("error: failed to connect to peer %d: %v", peerID, err)
					mu.Lock()
					allSuccessful = false
					mu.Unlock()
					return
				}

				rn.mu.Lock()
				rn.peerClients[peerID] = client
				rn.mu.Unlock()
			}

			req := ConfigUpdateRequest{
				Term:   rn.currentTerm,
				Leader: rn.id,
				Nodes:  rn.cluster.Nodes,
			}

			var resp ConfigUpdateResponse
			err := client.Call("RaftNode.UpdateConfigurationRPC", req, &resp)
			if err != nil {
				log.Printf("error: failed to update config on node %d: %v", peerID, err)
				mu.Lock()
				allSuccessful = false
				mu.Unlock()
				return
			}

			if !resp.Success {
				log.Printf("warn: node %d rejected config update, term: %d", peerID, resp.Term)
				mu.Lock()
				allSuccessful = false
				mu.Unlock()

				if resp.Term > rn.currentTerm {
					rn.handleHigherTerm(resp.Term)
				}
			} else {
				log.Printf("info: successfully updated config on node %d", peerID)
			}
		}(peer.ID, peer.Address)
	}

	wg.Wait()

	return allSuccessful
}
