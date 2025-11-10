package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
)

// NPCConfig represents a single NPC client configuration
type NPCConfig struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ServerAddr     string `json:"serverAddr"`
	VKey           string `json:"vkey"`
	ConnType       string `json:"connType"`
	ProxyURL       string `json:"proxyUrl"`
	LogLevel       string `json:"logLevel"`
	AutoReconnect  bool   `json:"autoReconnect"`
	SkipVerify     bool   `json:"skipVerify"`
	DisableP2P     bool   `json:"disableP2P"`
	ProtoVersion   int    `json:"protoVersion"`
	DNSServer      string `json:"dnsServer"`
	KeepAlive      int    `json:"keepAlive"`
	ConfigFilePath string `json:"configFilePath"`
	IsActive       bool   `json:"isActive"`
}

// NPCService manages NPC client operations
type NPCService struct {
	ctx          context.Context
	cancel       context.CancelFunc
	configs      map[string]*NPCConfig
	clients      map[string]*client.TRPClient
	configsMutex sync.RWMutex
	clientsMutex sync.RWMutex
	configDir    string
	logBuffer    []string
	logMutex     sync.RWMutex
}

// NewNPCService creates a new NPC service
func NewNPCService() *NPCService {
	ctx, cancel := context.WithCancel(context.Background())
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".npc-gui")
	os.MkdirAll(configDir, 0755)

	service := &NPCService{
		ctx:       ctx,
		cancel:    cancel,
		configs:   make(map[string]*NPCConfig),
		clients:   make(map[string]*client.TRPClient),
		configDir: configDir,
		logBuffer: make([]string, 0, 1000),
	}

	// Initialize logging
	logs.Init("off", "trace", "", 0, 0, 0, false, false)

	// Load saved configurations
	service.loadConfigs()

	return service
}

// GetVersion returns the NPC version
func (s *NPCService) GetVersion() string {
	return fmt.Sprintf("NPC Version: %s, Core Version: %s", version.VERSION, version.GetVersion(version.GetLatestIndex()))
}

// ListConfigs returns all saved configurations
func (s *NPCService) ListConfigs() []NPCConfig {
	s.configsMutex.RLock()
	defer s.configsMutex.RUnlock()

	configs := make([]NPCConfig, 0, len(s.configs))
	for _, cfg := range s.configs {
		configs = append(configs, *cfg)
	}
	return configs
}

// SaveConfig saves a new or updates an existing configuration
func (s *NPCService) SaveConfig(cfg NPCConfig) error {
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("cfg_%d", time.Now().Unix())
	}
	if cfg.Name == "" {
		return fmt.Errorf("configuration name is required")
	}
	if cfg.ServerAddr == "" {
		return fmt.Errorf("server address is required")
	}
	if cfg.VKey == "" {
		return fmt.Errorf("verification key is required")
	}

	// Set defaults
	if cfg.ConnType == "" {
		cfg.ConnType = "tcp"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.ProtoVersion == 0 {
		cfg.ProtoVersion = version.GetLatestIndex()
	}
	if cfg.DNSServer == "" {
		cfg.DNSServer = "8.8.8.8"
	}

	s.configsMutex.Lock()
	s.configs[cfg.ID] = &cfg
	s.configsMutex.Unlock()

	// Save to disk
	return s.saveConfigs()
}

// DeleteConfig deletes a configuration
func (s *NPCService) DeleteConfig(id string) error {
	s.configsMutex.Lock()
	defer s.configsMutex.Unlock()

	if _, exists := s.configs[id]; !exists {
		return fmt.Errorf("configuration not found")
	}

	// Stop client if running
	s.StopClient(id)

	delete(s.configs, id)
	return s.saveConfigs()
}

// StartClient starts an NPC client with the given configuration
func (s *NPCService) StartClient(id string) error {
	s.configsMutex.RLock()
	cfg, exists := s.configs[id]
	s.configsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("configuration not found")
	}

	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()

	// Check if already running
	if _, running := s.clients[id]; running {
		return fmt.Errorf("client is already running")
	}

	s.addLog(fmt.Sprintf("Starting client: %s", cfg.Name))

	// Set up client parameters
	client.Ver = cfg.ProtoVersion
	client.SkipTLSVerify = cfg.SkipVerify
	client.DisableP2P = cfg.DisableP2P
	client.AutoReconnect = cfg.AutoReconnect

	// Set DNS server
	common.SetCustomDNS(cfg.DNSServer)

	// Synchronize time
	common.SyncTime()

	// Parse server addresses, vkeys, and connection types
	serverAddrs := strings.Split(cfg.ServerAddr, ",")
	verifyKeys := strings.Split(cfg.VKey, ",")
	connTypes := strings.Split(cfg.ConnType, ",")

	serverAddrs = common.HandleArrEmptyVal(serverAddrs)
	verifyKeys = common.HandleArrEmptyVal(verifyKeys)
	connTypes = common.HandleArrEmptyVal(connTypes)

	if len(connTypes) == 0 {
		connTypes = append(connTypes, "tcp")
	}

	if len(serverAddrs) == 0 || len(verifyKeys) == 0 {
		return fmt.Errorf("invalid server address or verification key")
	}

	common.ExtendArrs(&serverAddrs, &verifyKeys, &connTypes)

	// Create and start client for the first server
	serverAddr := serverAddrs[0]
	verifyKey := verifyKeys[0]
	connType := strings.ToLower(connTypes[0])

	s.addLog(fmt.Sprintf("Connecting to server: %s with type: %s", serverAddr, connType))

	// Create new client
	cl := client.NewRPClient(serverAddr, verifyKey, connType, cfg.ProxyURL, "", nil, 60, nil)
	s.clients[id] = cl

	// Start client in a goroutine
	go func() {
		for {
			cl.Start(s.ctx)
			if !cfg.AutoReconnect {
				s.addLog(fmt.Sprintf("Client %s closed", cfg.Name))
				s.clientsMutex.Lock()
				delete(s.clients, id)
				s.clientsMutex.Unlock()
				return
			}
			s.addLog(fmt.Sprintf("Client %s disconnected, reconnecting in 5 seconds...", cfg.Name))
			time.Sleep(5 * time.Second)
		}
	}()

	// Mark as active
	s.configsMutex.Lock()
	cfg.IsActive = true
	s.configsMutex.Unlock()

	s.addLog(fmt.Sprintf("Client %s started successfully", cfg.Name))
	return nil
}

// StopClient stops a running NPC client
func (s *NPCService) StopClient(id string) error {
	s.clientsMutex.Lock()
	cl, exists := s.clients[id]
	if exists {
		delete(s.clients, id)
	}
	s.clientsMutex.Unlock()

	if !exists {
		return fmt.Errorf("client is not running")
	}

	s.configsMutex.Lock()
	if cfg, exists := s.configs[id]; exists {
		cfg.IsActive = false
		s.addLog(fmt.Sprintf("Stopping client: %s", cfg.Name))
	}
	s.configsMutex.Unlock()

	cl.Close()
	s.addLog("Client stopped successfully")
	return nil
}

// GetClientStatus returns the status of a client
func (s *NPCService) GetClientStatus(id string) map[string]interface{} {
	s.clientsMutex.RLock()
	_, running := s.clients[id]
	s.clientsMutex.RUnlock()

	s.configsMutex.RLock()
	cfg, exists := s.configs[id]
	s.configsMutex.RUnlock()

	status := map[string]interface{}{
		"running": running,
		"exists":  exists,
	}

	if exists {
		status["name"] = cfg.Name
		status["serverAddr"] = cfg.ServerAddr
		status["connType"] = cfg.ConnType
	}

	return status
}

// GetLogs returns recent log messages
func (s *NPCService) GetLogs() []string {
	s.logMutex.RLock()
	defer s.logMutex.RUnlock()

	// Return a copy of the log buffer
	logs := make([]string, len(s.logBuffer))
	copy(logs, s.logBuffer)
	return logs
}

// ClearLogs clears the log buffer
func (s *NPCService) ClearLogs() {
	s.logMutex.Lock()
	defer s.logMutex.Unlock()
	s.logBuffer = make([]string, 0, 1000)
}

// addLog adds a log message to the buffer
func (s *NPCService) addLog(message string) {
	s.logMutex.Lock()
	defer s.logMutex.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logMsg := fmt.Sprintf("[%s] %s", timestamp, message)
	
	s.logBuffer = append(s.logBuffer, logMsg)
	
	// Keep only last 1000 messages
	if len(s.logBuffer) > 1000 {
		s.logBuffer = s.logBuffer[len(s.logBuffer)-1000:]
	}
}

// loadConfigs loads configurations from disk
func (s *NPCService) loadConfigs() error {
	configFile := filepath.Join(s.configDir, "configs.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No configs yet
		}
		return err
	}

	var configs []NPCConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return err
	}

	s.configsMutex.Lock()
	defer s.configsMutex.Unlock()

	for i := range configs {
		configs[i].IsActive = false // Reset active status on load
		s.configs[configs[i].ID] = &configs[i]
	}

	s.addLog(fmt.Sprintf("Loaded %d configurations", len(configs)))
	return nil
}

// saveConfigs saves configurations to disk
func (s *NPCService) saveConfigs() error {
	configFile := filepath.Join(s.configDir, "configs.json")

	s.configsMutex.RLock()
	configs := make([]NPCConfig, 0, len(s.configs))
	for _, cfg := range s.configs {
		configs = append(configs, *cfg)
	}
	s.configsMutex.RUnlock()

	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configFile, data, 0644)
}

// Cleanup stops all clients and cleans up resources
func (s *NPCService) Cleanup() {
	s.clientsMutex.Lock()
	for id, cl := range s.clients {
		cl.Close()
		delete(s.clients, id)
	}
	s.clientsMutex.Unlock()

	s.cancel()
}
