package raft

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"strings"
	"syscall"
	"time"
)

const (
	maxRetries     = 10                     // Maximum retry attempts
	initialBackoff = 100 * time.Millisecond // Initial backoff time
)

func (rn *RaftNode) RPCSetup() error {
	listener, err := rn.StartListener()
	if err != nil {
		log.Printf("error: failed to start listener: %v", err)
		return err
	}
	rn.listener = listener

	err = rn.EstablishPeerConnections()
	if err != nil {
		log.Printf("error: failed to populate peer clients: %v", err)
		return err
	}

	return nil
}

func (rn *RaftNode) EstablishPeerConnections() error {
	// TODO: Optimize by using goroutines to establish peer connections in parallel
	for _, peer := range rn.peers {
		var client *rpc.Client
		var err error
		backoff := initialBackoff

		for attempt := 0; attempt < maxRetries; attempt++ {
			client, err = rpc.Dial("tcp", peer.Address)
			if err == nil {
				break
			}

			var opErr *net.OpError
			if errors.As(err, &opErr) && (opErr.Err.Error() == "connection refused" || errors.Is(opErr.Err, syscall.ECONNREFUSED)) {
				log.Printf("warn: connection refused by peer %v address %v, retrying in %v... (attempt %d/%d)", peer.ID, peer.Address, backoff, attempt+1, maxRetries)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}

			log.Printf("error: failed to connect to peer %v address %v: %v", peer.ID, peer.Address, err)
			return err
		}

		if err != nil {
			log.Printf("error: max retries reached for peer %v address %v", peer.ID, peer.Address)
			return err
		}

		rn.peerClients[peer.ID] = client
	}

	return nil
}

func (rn *RaftNode) StartListener() (net.Listener, error) {
	addr := rn.addr
	port := strings.Split(addr, ":")[1]
	listener, err := net.Listen("tcp", fmt.Sprintf(":%v", port))
	if err != nil {
		return nil, err
	}

	return listener, nil
}
