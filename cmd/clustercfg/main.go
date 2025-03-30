package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/arbha1erao/raft/clustercfg"
)

func main() {
	listenAddr := flag.String("addr", "localhost:6000", "Address for the Config Manager to listen on")
	configFile := flag.String("config", "cluster_config.json", "Path to the config file")
	quorumSize := flag.Int("quorum", 3, "Number of nodes required for quorum")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("Starting Config Manager on %s", *listenAddr)
	log.Printf("Using config file: %s", *configFile)
	log.Printf("Quorum size: %d nodes", *quorumSize)

	cm, err := clustercfg.NewConfigManager(*configFile, *listenAddr, *quorumSize)
	if err != nil {
		log.Fatalf("Failed to start Config Manager: %v", err)
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-signalChan
	log.Printf("Received signal %v, shutting down...", sig)

	if err := cm.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Config Manager stopped")
}
