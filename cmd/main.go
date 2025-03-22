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

	var clusterCfg raft.ClusterConfig
	err = utils.LoadTOMLConfig(flags.ConfigPath, &clusterCfg)
	if err != nil {
		log.Fatalf("error: failed to load config: %v", err)
	}

	var nodeCfg *raft.NodeConfig
	for _, n := range clusterCfg.Nodes {
		if n.ID == flags.NodeID {
			nodeCfg = &n
			break
		}
	}
	if nodeCfg == nil {
		log.Fatalf("error: NodeID %d not found in config", flags.NodeID)
	}

	log.Printf("info: loaded Cluster Config: %+v", clusterCfg)
	log.Printf("info: loaded Node Config: %+v", *nodeCfg)

	node := raft.NewRaftNode(*nodeCfg, clusterCfg.Nodes)

	go node.Run()

	for {
		time.Sleep(time.Second)
	}
}
