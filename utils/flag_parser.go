package utils

import (
	"flag"
	"fmt"
)

type Flags struct {
	NodeID     int
	ConfigPath string
}

func ParseFlags() (Flags, error) {
	nodeID := flag.Int("id", -1, "ID of the current node (Required)")
	configPath := flag.String("config", "", "Path to the config file (Required)")

	flag.Parse()

	if *nodeID == -1 || *configPath == "" {
		return Flags{}, fmt.Errorf("missing required flags")
	}

	return Flags{
		NodeID:     *nodeID,
		ConfigPath: *configPath,
	}, nil
}
