package main

import (
	"flag"
	"log"
	"time"

	"github.com/arbha1erao/raft/clustercfg"
	"github.com/arbha1erao/raft/raft"
)

func main() {
	nodeID := flag.Int("id", 0, "Node ID (required)")
	nodeAddress := flag.String("addr", "", "Node address (host:port) (required)")
	cmAddress := flag.String("cm", "http://localhost:6000", "ClusterConfigManager URL")
	waitQuorum := flag.Bool("wait-quorum", true, "Wait for cluster quorum before starting")
	quorumTimeout := flag.Duration("quorum-timeout", 5*time.Minute, "Timeout for waiting on quorum")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if *nodeID <= 0 {
		log.Fatalf("error: Node ID is required (-id flag)")
	}
	if *nodeAddress == "" {
		log.Fatalf("error: Node address is required (-addr flag)")
	}

	log.Printf("Starting Raft node %d at %s with ClusterConfigManager at %s", *nodeID, *nodeAddress, *cmAddress)

	cmClient := clustercfg.NewClient(*cmAddress, *nodeID, *nodeAddress)
	if cmClient == nil {
		log.Fatalf("error: Failed to create ClusterConfigManager client")
	}

	err := cmClient.Register()
	if err != nil {
		log.Fatalf("error: Failed to register with ClusterConfigManager: %v", err)
	}
	log.Printf("Successfully registered with ClusterConfigManager")

	// Wait for quorum if required
	if *waitQuorum {
		log.Printf("Waiting for cluster quorum (timeout: %s)...", quorumTimeout.String())
		hasQuorum := cmClient.WaitForQuorum(*quorumTimeout)
		if !hasQuorum {
			log.Printf("warn: Timed out waiting for quorum, continuing as standalone node")
		} else {
			log.Printf("Quorum reached! Proceeding with Raft initialization")
		}
	}

	nodes, err := cmClient.ListNodes()
	if err != nil {
		log.Fatalf("error: Failed to get nodes from ClusterConfigManager: %v", err)
	}

	var nodeConfigs []raft.NodeConfig
	for id, addr := range nodes {
		nodeConfigs = append(nodeConfigs, raft.NodeConfig{
			ID:      id,
			Address: addr,
		})
	}

	log.Printf("Initializing with %d nodes from ClusterConfigManager", len(nodeConfigs))

	nodeConfig := raft.NodeConfig{
		ID:      *nodeID,
		Address: *nodeAddress,
	}

	node := raft.NewRaftNode(nodeConfig, nodeConfigs, cmClient)

	err = node.RPCSetup()
	if err != nil {
		log.Fatalf("error: Failed to set up RPC: %v", err)
	}

	node.Run()
}
