package raft

import (
	"log"
	"net/rpc"
	"sync"
)

type State string

const (
	FOLLOWER  State = "FOLLOWER"
	CANDIDATE State = "CANDIDATE"
	LEADER    State = "LEADER"
)

type LogEntry struct {
	Term    int
	Command any
}

type ClusterConfig struct {
	ClusterSize int          `toml:"cluster_size"`
	Nodes       []NodeConfig `toml:"nodes"`
}

type NodeConfig struct {
	ID      int    `toml:"id"`
	Address string `toml:"address"`
}

type RaftNode struct {
	mu          sync.Mutex
	id          int
	peerIds     []int
	currentTerm int
	votedFor    int
	state       State
	log         []LogEntry
	peers       map[int]*rpc.Client
}

func NewRaftNode(ncfg NodeConfig, nodes []NodeConfig) *RaftNode {
	peerIDs := []int{}
	for _, node := range nodes {
		if node.ID != ncfg.ID {
			peerIDs = append(peerIDs, node.ID)
		}
	}

	return &RaftNode{
		id:          ncfg.ID,
		peerIds:     peerIDs,
		currentTerm: 0,
		votedFor:    -1,
		state:       FOLLOWER,
		log:         []LogEntry{},
		peers:       make(map[int]*rpc.Client),
	}
}

func (rn *RaftNode) Run() {
	log.Printf("infog: node %d starting in FOLLOWER state\n", rn.id)
}
