package raft

type State string

const (
	FOLLOWER  State = "FOLLOWER"
	CANDIDATE State = "CANDIDATE"
	LEADER    State = "LEADER"
)

type ConfigChangeType int

const (
	AddNode ConfigChangeType = iota
	RemoveNode
)

type ConfigChange struct {
	Type       ConfigChangeType
	NodeID     int
	NodeConfig NodeConfig
}

type Configuration struct {
	Nodes map[int]NodeConfig
}

type LogEntry struct {
	Term         int
	Command      any
	ConfigChange *ConfigChange
}

type ClusterConfig struct {
	ClusterSize int          `toml:"cluster_size"`
	Nodes       []NodeConfig `toml:"nodes"`
}

type NodeConfig struct {
	ID      int    `toml:"id"`
	Address string `toml:"address"`
}
