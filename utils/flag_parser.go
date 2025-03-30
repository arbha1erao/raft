package utils

import (
	"flag"
	"fmt"
)

type Flags struct {
	NodeID      int
	ConfigPath  string
	NodeAddress string
	JoinAddress string
}

func ParseFlags() (Flags, error) {
	nodeID := flag.Int("id", -1, "ID of the current node (Required)")
	configPath := flag.String("config", "", "Path to the config file (Required for existing nodes)")
	nodeAddress := flag.String("addr", "", "Address of this node (Required for new nodes)")
	joinAddress := flag.String("join", "", "Address of a node to join the cluster")

	flag.Parse()

	if *joinAddress != "" {
		if *nodeID == -1 {
			return Flags{}, fmt.Errorf("when joining a cluster, -id flag is required")
		}
		if *nodeAddress == "" {
			return Flags{}, fmt.Errorf("when joining a cluster, -addr flag is required")
		}
		return Flags{
			NodeID:      *nodeID,
			ConfigPath:  *configPath,
			NodeAddress: *nodeAddress,
			JoinAddress: *joinAddress,
		}, nil
	}

	if *nodeID == -1 || *configPath == "" {
		return Flags{}, fmt.Errorf("missing required flags: -id and -config are required")
	}

	return Flags{
		NodeID:      *nodeID,
		ConfigPath:  *configPath,
		NodeAddress: *nodeAddress,
		JoinAddress: *joinAddress,
	}, nil
}
