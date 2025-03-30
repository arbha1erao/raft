package main

import (
	"log"
	"time"

	"github.com/arbha1erao/raft/raft"
	"github.com/arbha1erao/raft/utils"
)

func main() {
	flags, err := utils.ParseFlags()
	if err != nil {
		log.Fatalf("error: failed to parse flags: %v", err)
	}

	isNewNode := flags.JoinAddress != ""

	var nodeCfg raft.NodeConfig
	var nodes []raft.NodeConfig

	if isNewNode {
		log.Printf("info: starting as a new node with ID %d, will join via %s", flags.NodeID, flags.JoinAddress)

		if flags.NodeAddress == "" {
			log.Fatalf("error: node address is required when joining a cluster (-addr flag)")
		}

		nodeCfg = raft.NodeConfig{
			ID:      flags.NodeID,
			Address: flags.NodeAddress,
		}

		nodes = []raft.NodeConfig{nodeCfg}
	} else {
		var clusterCfg raft.ClusterConfig
		err = utils.LoadTOMLConfig(flags.ConfigPath, &clusterCfg)
		if err != nil {
			log.Fatalf("error: failed to load config: %v", err)
		}

		var foundNode bool
		for _, n := range clusterCfg.Nodes {
			if n.ID == flags.NodeID {
				nodeCfg = n
				foundNode = true
				break
			}
		}

		if !foundNode {
			log.Fatalf("error: NodeID %d not found in config", flags.NodeID)
		}

		nodes = clusterCfg.Nodes

		log.Printf("info: loaded Cluster Config: %+v", clusterCfg)
		log.Printf("info: loaded Node Config: %+v", nodeCfg)
	}

	node := raft.NewRaftNode(nodeCfg, nodes)

	err = node.RPCSetup()
	if err != nil {
		log.Fatalf("error: failed to setup rpc: %v", err)
	}

	node.Run()

	// Keep the main thread alive
	for {
		time.Sleep(time.Second)
	}
}
