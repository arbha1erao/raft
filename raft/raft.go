package raft

import (
	"log"
	"net"
	"net/rpc"
	"sync"
	"time"

	"math/rand/v2"
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
	joinMode      bool

	mu sync.Mutex
}

func NewRaftNode(ncfg NodeConfig, nodes []NodeConfig) *RaftNode {
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

	return &RaftNode{
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
		joinMode:    false,

		heartbeatChan: make(chan int),
		stepDownChan:  make(chan int),
	}
}

func (rn *RaftNode) Run() {
	electionTimeout := time.NewTimer(randomElectionTimeout())
	heartbeatTicker := time.NewTicker(150 * time.Millisecond)

	log.Printf("info: node %v started as a %v", rn.id, rn.state)

	// If this is a new node starting (not in the initial config), attempt to join the cluster
	if rn.isNewNode() {
		rn.joinMode = true
		joinSucceeded := make(chan bool, 1)
		go func() {
			success := rn.joinCluster()
			joinSucceeded <- success
		}()

		select {
		case success := <-joinSucceeded:
			if !success {
				log.Printf("error: failed to join cluster after all attempts, continuing as standalone node")
			} else {
				log.Printf("info: successfully joined cluster, proceeding with normal operation")
			}
			rn.joinMode = false
		case <-time.After(10 * time.Second):
			log.Printf("error: timed out trying to join cluster, continuing as standalone node")
			rn.joinMode = false
		}
	}

	for {
		if rn.joinMode {
			time.Sleep(100 * time.Millisecond)
			continue
		}

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

// isNewNode determines if this is a new node joining an existing cluster
func (rn *RaftNode) isNewNode() bool {
	// Check if this node is set up with a single-node configuration
	// This is an indicator that it's a new node that needs to join
	return len(rn.peers) == 0 && rn.cluster.ClusterSize == 1
}

// joinCluster attempts to join an existing cluster by contacting known seed nodes
// Returns true if join was successful, false otherwise
func (rn *RaftNode) joinCluster() bool {
	// FIX ME
	// In a real implementation, have a list of seed nodes to try
	// For now, we'll simulate by trying to connect to nodes listed in config.toml

	// Read seed nodes from environment or use default ports
	seedAddresses := []string{
		"localhost:5001",
		"localhost:5002",
		"localhost:5003",
		"localhost:5004",
		"localhost:5005",
	}

	var filteredAddresses []string
	for _, addr := range seedAddresses {
		if addr != rn.addr {
			filteredAddresses = append(filteredAddresses, addr)
		}
	}

	joinRequest := JoinRequest{
		NodeID:   rn.id,
		NodeAddr: rn.addr,
	}

	for _, addr := range filteredAddresses {
		log.Printf("info: attempting to join cluster via %s", addr)

		if !verifyNodeConnectivity(addr) {
			log.Printf("warn: node at %s is not reachable, trying next node", addr)
			continue
		}

		client, err := rpc.Dial("tcp", addr)
		if err != nil {
			log.Printf("warn: failed to connect to %s: %v", addr, err)
			continue
		}

		var resp JoinResponse
		err = client.Call("RaftNode.JoinClusterRPC", joinRequest, &resp)
		if err != nil {
			log.Printf("warn: join request to %s failed: %v", addr, err)
			client.Close()
			continue
		}

		if resp.Success {
			log.Printf("info: successfully joined cluster via %s", addr)

			rn.mu.Lock()
			rn.cluster.Nodes = resp.Nodes
			rn.cluster.ClusterSize = len(resp.Nodes)
			rn.currentTerm = resp.CurrentTerm
			rn.leaderID = resp.CurrentLeader

			rn.rebuildPeersList()
			rn.mu.Unlock()

			rn.connectToNewPeers()

			client.Close()
			return true
		}

		if resp.CurrentLeader > 0 && resp.CurrentLeader != rn.id {
			log.Printf("info: redirected to leader %d", resp.CurrentLeader)

			var leaderAddr string
			for _, node := range resp.Nodes {
				if node.ID == resp.CurrentLeader {
					leaderAddr = node.Address
					break
				}
			}

			if leaderAddr != "" {
				client.Close()

				if !verifyNodeConnectivity(leaderAddr) {
					log.Printf("warn: leader at %s is not reachable, trying next node", leaderAddr)
					continue
				}

				client, err = rpc.Dial("tcp", leaderAddr)
				if err != nil {
					log.Printf("warn: failed to connect to leader at %s: %v", leaderAddr, err)
					continue
				}

				err = client.Call("RaftNode.JoinClusterRPC", joinRequest, &resp)
				if err != nil {
					log.Printf("warn: join request to leader failed: %v", err)
					client.Close()
					continue
				}

				if resp.Success {
					log.Printf("info: successfully joined cluster via leader %d", resp.CurrentLeader)

					rn.mu.Lock()
					rn.cluster.Nodes = resp.Nodes
					rn.cluster.ClusterSize = len(resp.Nodes)
					rn.currentTerm = resp.CurrentTerm
					rn.leaderID = resp.CurrentLeader

					rn.rebuildPeersList()
					rn.mu.Unlock()

					rn.connectToNewPeers()

					client.Close()
					return true
				}
			}
		}

		client.Close()
	}

	log.Printf("warn: failed to join cluster, will continue as standalone node")
	return false
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
		rn.currentTerm = term
		rn.state = FOLLOWER
		rn.votedFor = -1
		rn.leaderID = -1

		if rn.state == LEADER || rn.state == CANDIDATE {
			go func() {
				rn.stepDownChan <- 1
			}()
		}
	}
	rn.mu.Unlock()
}
