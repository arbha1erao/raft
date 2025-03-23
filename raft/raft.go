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

	currentTerm int
	votedFor    int
	state       State
	log         []LogEntry

	heartbeatChan chan int
	stepDownChan  chan int
	listener      net.Listener

	mu sync.Mutex
}

func NewRaftNode(ncfg NodeConfig, nodes []NodeConfig) *RaftNode {
	var peers []NodeConfig
	for _, node := range nodes {
		if node.ID != ncfg.ID {
			peers = append(peers, node)
		}
	}

	return &RaftNode{
		id:          ncfg.ID,
		addr:        ncfg.Address,
		peers:       peers,
		peerClients: make(map[int]*rpc.Client),

		currentTerm: 0,
		votedFor:    -1,
		state:       FOLLOWER,
		log:         []LogEntry{},

		heartbeatChan: make(chan int),
		stepDownChan:  make(chan int),
	}
}

func (rn *RaftNode) Run() {
	electionTimeout := time.NewTimer(randomElectionTimeout())
	heartbeatTicker := time.NewTicker(150 * time.Millisecond)

	log.Printf("info: node %v started as a %v", rn.id, rn.state)

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

func (rn *RaftNode) startElection() {
	rn.mu.Lock()
	rn.currentTerm++
	rn.votedFor = rn.id
	rn.mu.Unlock()

	votes := 1
	for _, peer := range rn.peers {
		args := RequestVoteArgs{Term: rn.currentTerm, CandidateID: rn.id}
		reply := RequestVoteReply{}
		// TODO: handle error
		err := rn.peerClients[peer.ID].Call("RaftNode.RequestVoteRPC", args, &reply)
		if err != nil {
			log.Printf("error: failed to contact peer %d for vote: %v", peer.ID, err)
			continue
		}
		if reply.VoteGranted {
			votes++
		}
	}

	if votes > len(rn.peers)/2 {
		log.Printf("info: node %d is now the leader", rn.id)
		rn.mu.Lock()
		rn.state = LEADER
		rn.mu.Unlock()
		rn.sendHeartbeats()
	}
}

func (rn *RaftNode) sendHeartbeats() {
	heartbeat := []LogEntry{}

	for _, peer := range rn.peers {
		args := AppendEntriesArgs{
			Term:     rn.currentTerm,
			LeaderID: rn.id,
			Entries:  heartbeat,
		}
		reply := AppendEntriesReply{}

		// TODO: handle error
		err := rn.peerClients[peer.ID].Call("RaftNode.AppendEntriesRPC", &args, &reply)
		if err != nil {
			log.Printf("error: failed to send heartbeat to %d: %v", peer.ID, err)
			continue
		}

		if reply.Term > rn.currentTerm {
			log.Printf("warning: leader %d detected higher term %d from %d, stepping down", rn.id, reply.Term, peer.ID)
			rn.mu.Lock()
			rn.currentTerm = reply.Term
			rn.state = FOLLOWER
			rn.mu.Unlock()
			rn.stepDownChan <- 1
			return
		}
	}
}

func randomElectionTimeout() time.Duration {
	return time.Duration(300+rand.Int64N(200)) * time.Millisecond
}
