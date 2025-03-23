package raft

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
