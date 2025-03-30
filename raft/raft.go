package raft

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"sync"
	"time"

	"math/rand/v2"

	"github.com/arbha1erao/raft/clustercfg"
)

type RaftNode struct {
	id          int
	addr        string
	peers       []NodeConfig
	peerClients map[int]*rpc.Client
	cluster     ClusterConfig

	currentTerm int
	votedFor    int
	state       State
	log         []LogEntry
	leaderID    int

	heartbeatChan chan int
	stepDownChan  chan int
	listener      net.Listener

	configClient     *clustercfg.Client
	configUpdateChan chan []NodeConfig

	mu sync.Mutex
}

func NewRaftNode(ncfg NodeConfig, nodes []NodeConfig, configClient *clustercfg.Client) *RaftNode {
	if configClient == nil {
		log.Fatalf("error: ClusterConfigManager is required, cannot create RaftNode without it")
	}

	var peers []NodeConfig
	for _, node := range nodes {
		if node.ID != ncfg.ID {
			peers = append(peers, node)
		}
	}

	cluster := ClusterConfig{
		ClusterSize: len(nodes),
		Nodes:       nodes,
	}

	rn := &RaftNode{
		id:          ncfg.ID,
		addr:        ncfg.Address,
		peers:       peers,
		peerClients: make(map[int]*rpc.Client),
		cluster:     cluster,

		currentTerm: 0,
		votedFor:    -1,
		state:       FOLLOWER,
		log:         []LogEntry{},
		leaderID:    -1,

		heartbeatChan: make(chan int, 100),
		stepDownChan:  make(chan int, 10),

		configClient:     configClient,
		configUpdateChan: make(chan []NodeConfig, 10),
	}

	configClient.StartConfigUpdateLoop(10*time.Second, func(config clustercfg.ClusterConfig) {
		var updatedConfigs []NodeConfig
		for id, addr := range config.Nodes {
			updatedConfigs = append(updatedConfigs, NodeConfig{
				ID:      id,
				Address: addr,
			})
		}

		log.Printf("info: received configuration update from ClusterConfigManager with %d nodes", len(updatedConfigs))

		select {
		case rn.configUpdateChan <- updatedConfigs:
		default:
			log.Printf("warn: config update channel full, skipping update")
		}
	})

	return rn
}

func (rn *RaftNode) Run() {
	electionTimeout := time.NewTimer(randomElectionTimeout())
	heartbeatTicker := time.NewTicker(150 * time.Millisecond)

	log.Printf("info: node %v started as a %v", rn.id, rn.state)

	log.Printf("info: node %v waiting for cluster quorum via ClusterConfigManager", rn.id)
	quorumReached := rn.configClient.WaitForQuorum(5 * time.Minute)
	if !quorumReached {
		log.Printf("warn: timed out waiting for quorum, continuing as standalone node")
	} else {
		log.Printf("info: quorum reached, proceeding with Raft protocol")

		_, err := rn.configClient.GetConfig()
		if err != nil {
			log.Printf("error: failed to get config: %v", err)
		} else {
			cmNodes := rn.configClient.GetNodeConfig()
			var nodes []NodeConfig
			for _, n := range cmNodes {
				nodes = append(nodes, NodeConfig{
					ID:      n.ID,
					Address: n.Address,
				})
			}
			rn.UpdateClusterConfiguration(nodes)
		}
	}

	go func() {
		for updatedNodes := range rn.configUpdateChan {
			rn.UpdateClusterConfiguration(updatedNodes)
		}
	}()

	// Add a small random delay before starting election loop to reduce simultaneous elections
	time.Sleep(time.Duration(rand.Int64N(500)) * time.Millisecond)

	for {
		rn.mu.Lock()
		switch rn.state {
		case FOLLOWER:
			rn.mu.Unlock()
			select {
			case <-electionTimeout.C:
				log.Printf("info: node %v election timeout, becoming candidate", rn.id)
				rn.mu.Lock()
				rn.state = CANDIDATE
				rn.mu.Unlock()
				rn.startElection()
				electionTimeout.Reset(randomElectionTimeout())

			case <-rn.heartbeatChan:
				electionTimeout.Reset(randomElectionTimeout())
			}

		case CANDIDATE:
			rn.mu.Unlock()
			select {
			case <-electionTimeout.C:
				log.Printf("info: node %v election timed out, retrying election", rn.id)
				rn.startElection()
				electionTimeout.Reset(randomElectionTimeout())
			case <-rn.heartbeatChan:
				log.Printf("info: node %v stepping down to follower", rn.id)
				rn.mu.Lock()
				rn.state = FOLLOWER
				rn.mu.Unlock()
				electionTimeout.Reset(randomElectionTimeout())
			case <-rn.stepDownChan:
				log.Printf("info: candidate %v detected higher term, stepping down", rn.id)
				rn.mu.Lock()
				rn.state = FOLLOWER
				rn.mu.Unlock()
				electionTimeout.Reset(randomElectionTimeout())
			}

		case LEADER:
			rn.mu.Unlock()
			select {
			case <-heartbeatTicker.C:
				rn.sendHeartbeats()
			case <-rn.stepDownChan:
				log.Printf("info: leader %v stepping down to follower", rn.id)
				rn.mu.Lock()
				rn.state = FOLLOWER
				rn.mu.Unlock()
				electionTimeout.Reset(randomElectionTimeout())
			}
		}
	}
}

// UpdateClusterConfiguration updates the cluster configuration with the provided nodes
func (rn *RaftNode) UpdateClusterConfiguration(nodes []NodeConfig) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	log.Printf("info: updating cluster configuration with %d nodes", len(nodes))

	rn.cluster.Nodes = nodes
	rn.cluster.ClusterSize = len(nodes)

	rn.rebuildPeersList()

	rn.connectToAllPeers()

	log.Printf("info: cluster configuration updated successfully")
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

func (rn *RaftNode) startElection() {
	rn.mu.Lock()
	rn.currentTerm++
	rn.votedFor = rn.id
	currentTerm := rn.currentTerm
	rn.mu.Unlock()

	votes := 1
	var voteMutex sync.Mutex
	var wg sync.WaitGroup

	for _, peer := range rn.peers {
		if _, exists := rn.peerClients[peer.ID]; !exists {
			log.Printf("warn: no connection to peer %d for vote request", peer.ID)
			continue
		}

		wg.Add(1)
		go func(peerID int, client *rpc.Client) {
			defer wg.Done()

			args := RequestVoteArgs{
				Term:        currentTerm,
				CandidateID: rn.id,
				// FIX ME
				// Last log details would go here in a full implementation
			}
			reply := RequestVoteReply{}

			err := client.Call("RaftNode.RequestVoteRPC", args, &reply)
			if err != nil {
				log.Printf("error: failed to contact peer %d for vote: %v", peerID, err)
				return
			}

			if reply.Term > currentTerm {
				rn.handleHigherTerm(reply.Term)
				return
			}

			if reply.VoteGranted {
				voteMutex.Lock()
				votes++
				voteMutex.Unlock()
			}
		}(peer.ID, rn.peerClients[peer.ID])
	}

	wg.Wait()

	rn.mu.Lock()
	if rn.state != CANDIDATE || rn.currentTerm != currentTerm {
		rn.mu.Unlock()
		return
	}

	if votes > (len(rn.peers)+1)/2 {
		log.Printf("info: node %d is now the leader (term %d)", rn.id, rn.currentTerm)
		rn.state = LEADER
		rn.leaderID = rn.id
		rn.mu.Unlock()
		rn.sendHeartbeats()
	} else {
		log.Printf("info: election failed with %d votes out of %d needed", votes, (len(rn.peers)+1)/2+1)
		rn.mu.Unlock()
	}
}

func (rn *RaftNode) sendHeartbeats() {
	rn.mu.Lock()
	currentTerm := rn.currentTerm
	rn.mu.Unlock()

	heartbeat := []LogEntry{}
	var wg sync.WaitGroup

	for _, peer := range rn.peers {
		// Skip peers without an established connection
		client, exists := rn.peerClients[peer.ID]
		if !exists {
			continue
		}

		wg.Add(1)
		go func(peerID int, peerClient *rpc.Client) {
			defer wg.Done()

			args := AppendEntriesArgs{
				Term:     currentTerm,
				LeaderID: rn.id,
				Entries:  heartbeat,
			}
			reply := AppendEntriesReply{}

			err := peerClient.Call("RaftNode.AppendEntriesRPC", &args, &reply)
			if err != nil {
				log.Printf("error: failed to send heartbeat to %d: %v", peerID, err)
				return
			}

			if reply.Term > currentTerm {
				log.Printf("warning: leader %d detected higher term %d from %d, stepping down", rn.id, reply.Term, peerID)
				rn.handleHigherTerm(reply.Term)
				go func() {
					rn.stepDownChan <- 1
				}()
			}
		}(peer.ID, client)
	}

	// Don't wait for all heartbeats to complete - they should happen in background
}

func randomElectionTimeout() time.Duration {
	return time.Duration(300+rand.Int64N(200)) * time.Millisecond
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

// verifyNodeConnectivity attempts to establish a connection to the specified address and returns whether the connection was successful
func verifyNodeConnectivity(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		log.Printf("warn: cannot connect to %s: %v", addr, err)
		return false
	}
	conn.Close()
	return true
}

// handleHigherTerm is called when we discover a higher term
// Caller must NOT hold the lock
func (rn *RaftNode) handleHigherTerm(term int) {
	rn.mu.Lock()
	if term > rn.currentTerm {
		wasLeader := (rn.state == LEADER)
		oldTerm := rn.currentTerm

		rn.currentTerm = term
		rn.state = FOLLOWER
		rn.votedFor = -1
		rn.leaderID = -1

		if wasLeader {
			log.Printf("info: node %d was leader for term %d, stepping down to follower for term %d",
				rn.id, oldTerm, term)
		} else {
			log.Printf("info: node %d becoming follower for term %d", rn.id, term)
		}

		if rn.state == LEADER || rn.state == CANDIDATE {
			go func() {
				rn.stepDownChan <- 1
			}()
		}
	}
	rn.mu.Unlock()
}
