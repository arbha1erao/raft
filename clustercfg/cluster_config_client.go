package clustercfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type NodeConfig struct {
	ID      int    `json:"id"`
	Address string `json:"address"`
}

type Client struct {
	serverURL  string
	nodeID     int
	nodeAddr   string
	config     ClusterConfig
	updateChan chan ClusterConfig
	httpClient *http.Client
	clientID   string
}

func NewClient(serverURL string, nodeID int, nodeAddr string) *Client {
	clientID := fmt.Sprintf("node-%d-%d", nodeID, time.Now().UnixNano())
	return &Client{
		serverURL:  serverURL,
		nodeID:     nodeID,
		nodeAddr:   nodeAddr,
		updateChan: make(chan ClusterConfig, 10),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		clientID: clientID,
	}
}

// Register registers this node with the ConfigManager
func (c *Client) Register() error {
	reqBody := struct {
		NodeID  int    `json:"node_id"`
		Address string `json:"address"`
	}{
		NodeID:  c.nodeID,
		Address: c.nodeAddr,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Post(c.serverURL+"/register", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to register with config manager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to register: status %d", resp.StatusCode)
	}

	var registerResp struct {
		Success bool          `json:"success"`
		Config  ClusterConfig `json:"config"`
		Quorum  bool          `json:"quorum_reached"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&registerResp); err != nil {
		return err
	}

	if !registerResp.Success {
		return fmt.Errorf("registration failed")
	}

	c.config = registerResp.Config
	log.Printf("Client: Successfully registered with Config Manager. Current cluster size: %d", len(c.config.Nodes))
	return nil
}

// Deregister removes this node from the ConfigManager
func (c *Client) Deregister() error {
	reqBody := struct {
		NodeID int `json:"node_id"`
	}{
		NodeID: c.nodeID,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Post(c.serverURL+"/deregister", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to deregister with config manager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to deregister: status %d", resp.StatusCode)
	}

	log.Printf("Client: Successfully deregistered from Config Manager")
	return nil
}

// GetConfig retrieves the current configuration
func (c *Client) GetConfig() (ClusterConfig, error) {
	resp, err := c.httpClient.Get(c.serverURL + "/config")
	if err != nil {
		return ClusterConfig{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ClusterConfig{}, fmt.Errorf("failed to get config: status %d", resp.StatusCode)
	}

	var configResp struct {
		Success bool          `json:"success"`
		Config  ClusterConfig `json:"config"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&configResp); err != nil {
		return ClusterConfig{}, err
	}

	c.config = configResp.Config
	return c.config, nil
}

// ListNodes returns a list of all nodes in the cluster
func (c *Client) ListNodes() (map[int]string, error) {
	resp, err := c.httpClient.Get(c.serverURL + "/nodes")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list nodes: status %d", resp.StatusCode)
	}

	var listResp struct {
		Success bool           `json:"success"`
		Nodes   map[int]string `json:"nodes"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, err
	}

	return listResp.Nodes, nil
}

// WaitForQuorum blocks until the cluster has enough nodes for quorum
func (c *Client) WaitForQuorum(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	log.Printf("Client: Waiting for quorum...")

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			resp, err := c.httpClient.Get(c.serverURL + "/config")
			if err != nil {
				log.Printf("Client: Error checking quorum: %v", err)
				continue
			}

			var configResp struct {
				Success bool          `json:"success"`
				Quorum  bool          `json:"quorum_reached"`
				Config  ClusterConfig `json:"config"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&configResp); err != nil {
				resp.Body.Close()
				log.Printf("Client: Error parsing response: %v", err)
				continue
			}
			resp.Body.Close()

			c.config = configResp.Config
			nodeCount := len(configResp.Config.Nodes)

			if configResp.Quorum {
				log.Printf("Client: Quorum reached with %d nodes", nodeCount)
				return true
			}

			log.Printf("Client: Waiting for quorum, currently have %d nodes", nodeCount)
		}
	}

	log.Printf("Client: Timed out waiting for quorum")
	return false
}

// StartConfigUpdateLoop starts a background goroutine that periodically, checks for configuration updates
func (c *Client) StartConfigUpdateLoop(updateInterval time.Duration, callback func(ClusterConfig)) {
	ticker := time.NewTicker(updateInterval)

	go func() {
		for range ticker.C {
			config, err := c.GetConfig()
			if err != nil {
				log.Printf("Client: Error updating config: %v", err)
				continue
			}

			if config.Version > c.config.Version {
				c.config = config
				log.Printf("Client: Configuration updated to version %d with %d nodes",
					config.Version, len(config.Nodes))

				if callback != nil {
					callback(config)
				}
			}
		}
	}()
}

// GetNodeConfig converts the ClusterConfig to a slice of NodeConfig for Raft
func (c *Client) GetNodeConfig() []NodeConfig {
	var nodes []NodeConfig
	for id, addr := range c.config.Nodes {
		nodes = append(nodes, NodeConfig{
			ID:      id,
			Address: addr,
		})
	}
	return nodes
}
