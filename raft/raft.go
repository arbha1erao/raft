package raft

import (
	"log"
	"net"
	"net/rpc"
)

type RaftNode struct {
	// mu          sync.Mutex
	id          int
	addr        string
	peers       []NodeConfig
	currentTerm int
	votedFor    int
	state       State
	log         []LogEntry

	listener    net.Listener
	peerClients map[int]*rpc.Client
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
		currentTerm: 0,
		votedFor:    -1,
		state:       FOLLOWER,
		log:         []LogEntry{},
		peerClients: make(map[int]*rpc.Client),
	}
}

func (rn *RaftNode) Run() {
	log.Printf("info: node %v is ready. Starting Raft", rn.id)

}
