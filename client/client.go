package main

import (
	"flag"
	"fmt"
	"log"
	"net/rpc"
	"os"
)

type AddServerArgs struct {
	NewNodeID  int
	NewAddress string
}

type AddServerReply struct {
	Term    int
	Success bool
}

type RemoveServerArgs struct {
	NodeID int
}

type RemoveServerReply struct {
	Term    int
	Success bool
}

func main() {
	leaderAddr := flag.String("leader", "localhost:5001", "Address of the Raft leader")
	operation := flag.String("op", "", "Operation to perform: add or remove")
	nodeID := flag.Int("id", 0, "Node ID for the operation")
	nodeAddr := flag.String("addr", "", "Node address (required for add operation)")

	flag.Parse()

	if *operation != "add" && *operation != "remove" {
		fmt.Println("Error: Operation must be either 'add' or 'remove'")
		flag.Usage()
		os.Exit(1)
	}

	if *nodeID == 0 {
		fmt.Println("Error: Node ID must be specified and greater than 0")
		flag.Usage()
		os.Exit(1)
	}

	if *operation == "add" && *nodeAddr == "" {
		fmt.Println("Error: Node address must be specified for add operation")
		flag.Usage()
		os.Exit(1)
	}

	client, err := rpc.Dial("tcp", *leaderAddr)
	if err != nil {
		log.Fatalf("Error connecting to leader at %s: %v", *leaderAddr, err)
	}
	defer client.Close()

	if *operation == "add" {
		args := AddServerArgs{
			NewNodeID:  *nodeID,
			NewAddress: *nodeAddr,
		}
		reply := AddServerReply{}

		err := client.Call("RaftNode.AddServerRPC", args, &reply)
		if err != nil {
			log.Fatalf("Error calling AddServerRPC: %v", err)
		}

		if reply.Success {
			fmt.Printf("Successfully added node %d at %s\n", *nodeID, *nodeAddr)
		} else {
			fmt.Printf("Failed to add node. Current term is %d\n", reply.Term)
		}
	} else if *operation == "remove" {
		args := RemoveServerArgs{
			NodeID: *nodeID,
		}
		reply := RemoveServerReply{}

		err := client.Call("RaftNode.RemoveServerRPC", args, &reply)
		if err != nil {
			log.Fatalf("Error calling RemoveServerRPC: %v", err)
		}

		if reply.Success {
			fmt.Printf("Successfully removed node %d\n", *nodeID)
		} else {
			fmt.Printf("Failed to remove node. Current term is %d\n", reply.Term)
		}
	}
}
