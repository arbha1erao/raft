package raft

import (
	"log"
	"sync"
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

// propagateConfigChange sends the updated configuration to all peers
// Returns true if all updates were successful
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
