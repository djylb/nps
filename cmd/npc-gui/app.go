package main

import (
	"context"
)

// App struct
type App struct {
	ctx        context.Context
	npcService *NPCService
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		npcService: NewNPCService(),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// shutdown is called when the app is closing
func (a *App) shutdown(ctx context.Context) {
	if a.npcService != nil {
		a.npcService.Cleanup()
	}
}

// GetVersion returns the NPC version
func (a *App) GetVersion() string {
	return a.npcService.GetVersion()
}

// ListConfigs returns all saved configurations
func (a *App) ListConfigs() []NPCConfig {
	return a.npcService.ListConfigs()
}

// SaveConfig saves a new or updates an existing configuration
func (a *App) SaveConfig(cfg NPCConfig) error {
	return a.npcService.SaveConfig(cfg)
}

// DeleteConfig deletes a configuration
func (a *App) DeleteConfig(id string) error {
	return a.npcService.DeleteConfig(id)
}

// StartClient starts an NPC client with the given configuration
func (a *App) StartClient(id string) error {
	return a.npcService.StartClient(id)
}

// StopClient stops a running NPC client
func (a *App) StopClient(id string) error {
	return a.npcService.StopClient(id)
}

// GetClientStatus returns the status of a client
func (a *App) GetClientStatus(id string) map[string]interface{} {
	return a.npcService.GetClientStatus(id)
}

// GetLogs returns recent log messages
func (a *App) GetLogs() []string {
	return a.npcService.GetLogs()
}

// ClearLogs clears the log buffer
func (a *App) ClearLogs() {
	a.npcService.ClearLogs()
}
