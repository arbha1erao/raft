package clustercfg

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type ClusterConfig struct {
	Nodes    map[int]string
	Version  int
	Modified time.Time
}

type ConfigManager struct {
	config        ClusterConfig
	configPath    string
	mu            sync.RWMutex
	subscribers   map[string]chan ClusterConfig
	httpServer    *http.Server
	quorumReached bool
	quorumSize    int
}

// NewConfigManager creates a new ConfigManager with the given config file path
func NewConfigManager(configPath string, listenAddr string, quorumSize int) (*ConfigManager, error) {
	cm := &ConfigManager{
		config: ClusterConfig{
			Nodes:    make(map[int]string),
			Version:  0,
			Modified: time.Now(),
		},
		configPath:    configPath,
		subscribers:   make(map[string]chan ClusterConfig),
		quorumReached: false,
		quorumSize:    quorumSize,
	}

	err := cm.loadConfig()
	if err != nil {
		if os.IsNotExist(err) {
			err = cm.saveConfig()
			if err != nil {
				return nil, fmt.Errorf("failed to create config file: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
	}

	// Check if quorum is already reached
	cm.quorumReached = len(cm.config.Nodes) >= cm.quorumSize

	mux := http.NewServeMux()
	mux.HandleFunc("/register", cm.handleRegister)
	mux.HandleFunc("/deregister", cm.handleDeregister)
	mux.HandleFunc("/config", cm.handleGetConfig)
	mux.HandleFunc("/nodes", cm.handleListNodes)
	mux.HandleFunc("/subscribe", cm.handleSubscribe)

	cm.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("Config Manager: HTTP server starting on %s", listenAddr)
		if err := cm.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Config Manager: HTTP server failed: %v", err)
		}
	}()

	return cm, nil
}

// loadConfig loads the configuration from the file
func (cm *ConfigManager) loadConfig() error {
	file, err := os.ReadFile(cm.configPath)
	if err != nil {
		return err
	}

	var config ClusterConfig
	err = json.Unmarshal(file, &config)
	if err != nil {
		return err
	}

	cm.mu.Lock()
	cm.config = config
	cm.mu.Unlock()

	log.Printf("Config Manager: loaded configuration with %d nodes", len(config.Nodes))
	return nil
}

// saveConfig saves the configuration to the file
func (cm *ConfigManager) saveConfig() error {
	cm.mu.RLock()
	config := cm.config
	cm.mu.RUnlock()

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cm.configPath, data, 0644)
}

// AddNode adds a node to the configuration
func (cm *ConfigManager) AddNode(nodeID int, address string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if addr, exists := cm.config.Nodes[nodeID]; exists && addr == address {
		return false
	}

	cm.config.Nodes[nodeID] = address
	cm.config.Version++
	cm.config.Modified = time.Now()

	// Check if quorum reached
	if !cm.quorumReached && len(cm.config.Nodes) >= cm.quorumSize {
		cm.quorumReached = true
		log.Printf("Config Manager: quorum reached with %d nodes", len(cm.config.Nodes))
	}

	go cm.saveConfig()

	go cm.notifySubscribers()

	log.Printf("Config Manager: added/updated node %d at %s", nodeID, address)
	return true
}

// RemoveNode removes a node from the configuration
func (cm *ConfigManager) RemoveNode(nodeID int) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.config.Nodes[nodeID]; !exists {
		return false
	}

	delete(cm.config.Nodes, nodeID)
	cm.config.Version++
	cm.config.Modified = time.Now()

	// Check if quorum lost quorum
	if cm.quorumReached && len(cm.config.Nodes) < cm.quorumSize {
		cm.quorumReached = false
		log.Printf("Config Manager: quorum lost, down to %d nodes", len(cm.config.Nodes))
	}

	go cm.saveConfig()

	go cm.notifySubscribers()

	log.Printf("Config Manager: removed node %d", nodeID)
	return true
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() ClusterConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

// Subscribe adds a subscriber to receive configuration updates
func (cm *ConfigManager) Subscribe(id string) chan ClusterConfig {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	ch := make(chan ClusterConfig, 10)
	cm.subscribers[id] = ch

	go func() {
		ch <- cm.config
	}()

	log.Printf("Config Manager: client %s subscribed to updates", id)
	return ch
}

// Unsubscribe removes a subscriber
func (cm *ConfigManager) Unsubscribe(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if ch, exists := cm.subscribers[id]; exists {
		close(ch)
		delete(cm.subscribers, id)
		log.Printf("Config Manager: client %s unsubscribed", id)
	}
}

// notifySubscribers sends the updated configuration to all subscribers
func (cm *ConfigManager) notifySubscribers() {
	cm.mu.RLock()
	config := cm.config
	subscribers := make(map[string]chan ClusterConfig, len(cm.subscribers))
	for id, ch := range cm.subscribers {
		subscribers[id] = ch
	}
	cm.mu.RUnlock()

	for id, ch := range subscribers {
		select {
		case ch <- config:
			// Successfully sent
		default:
			// Channel full or closed, remove subscriber
			log.Printf("Config Manager: removing unresponsive subscriber %s", id)
			cm.Unsubscribe(id)
		}
	}
}

// HasQuorum returns true if the cluster has enough nodes for quorum
func (cm *ConfigManager) HasQuorum() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.quorumReached
}

// HTTP handlers

func (cm *ConfigManager) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID  int    `json:"node_id"`
		Address string `json:"address"`
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.NodeID <= 0 || req.Address == "" {
		http.Error(w, "Invalid node ID or address", http.StatusBadRequest)
		return
	}

	host, port, err := net.SplitHostPort(req.Address)
	if err != nil || host == "" || port == "" {
		http.Error(w, "Invalid address format", http.StatusBadRequest)
		return
	}

	changed := cm.AddNode(req.NodeID, req.Address)

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Success bool          `json:"success"`
		Changed bool          `json:"changed"`
		Config  ClusterConfig `json:"config"`
		Quorum  bool          `json:"quorum_reached"`
	}{
		Success: true,
		Changed: changed,
		Config:  cm.GetConfig(),
		Quorum:  cm.HasQuorum(),
	}

	json.NewEncoder(w).Encode(resp)
}

func (cm *ConfigManager) handleDeregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID int `json:"node_id"`
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.NodeID <= 0 {
		http.Error(w, "Invalid node ID", http.StatusBadRequest)
		return
	}

	removed := cm.RemoveNode(req.NodeID)

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Success bool          `json:"success"`
		Removed bool          `json:"removed"`
		Config  ClusterConfig `json:"config"`
		Quorum  bool          `json:"quorum_reached"`
	}{
		Success: true,
		Removed: removed,
		Config:  cm.GetConfig(),
		Quorum:  cm.HasQuorum(),
	}

	json.NewEncoder(w).Encode(resp)
}

func (cm *ConfigManager) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Success bool          `json:"success"`
		Config  ClusterConfig `json:"config"`
		Quorum  bool          `json:"quorum_reached"`
	}{
		Success: true,
		Config:  cm.GetConfig(),
		Quorum:  cm.HasQuorum(),
	}

	json.NewEncoder(w).Encode(resp)
}

func (cm *ConfigManager) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	config := cm.GetConfig()

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Success bool           `json:"success"`
		Nodes   map[int]string `json:"nodes"`
		Count   int            `json:"count"`
		Quorum  bool           `json:"quorum_reached"`
	}{
		Success: true,
		Nodes:   config.Nodes,
		Count:   len(config.Nodes),
		Quorum:  cm.HasQuorum(),
	}

	json.NewEncoder(w).Encode(resp)
}

func (cm *ConfigManager) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	// FIX ME
	// This endpoint would normally use WebSockets for real-time updates
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Success bool          `json:"success"`
		Config  ClusterConfig `json:"config"`
		Message string        `json:"message"`
	}{
		Success: true,
		Config:  cm.GetConfig(),
		Message: "For real-time updates, use the client library",
	}

	json.NewEncoder(w).Encode(resp)
}

// Shutdown stops the config manager
func (cm *ConfigManager) Shutdown() error {
	if cm.httpServer != nil {
		return cm.httpServer.Close()
	}
	return nil
}
