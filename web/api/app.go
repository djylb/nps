package api

import (
	"context"
	"strings"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

type RequestMetadata struct {
	NodeID    string `json:"node_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Source    string `json:"source,omitempty"`
}

type Event struct {
	Name     string                 `json:"name"`
	Resource string                 `json:"resource,omitempty"`
	Action   string                 `json:"action,omitempty"`
	Actor    *Actor                 `json:"actor,omitempty"`
	Metadata RequestMetadata        `json:"metadata"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
}

type Hooks interface {
	OnManagementEvent(context.Context, Event) error
}

type NoopHooks struct{}

func (NoopHooks) OnManagementEvent(context.Context, Event) error {
	return nil
}

type App struct {
	NodeID         string
	Hooks          Hooks
	Services       webservice.Services
	ConfigProvider func() *servercfg.Snapshot
}

type Options struct {
	NodeID            string
	Hooks             Hooks
	Services          *webservice.Services
	ConfigureServices func(*webservice.Services)
	ConfigProvider    func() *servercfg.Snapshot
}

func New(cfg *servercfg.Snapshot) *App {
	return NewWithOptions(cfg, Options{})
}

func NewWithOptions(cfg *servercfg.Snapshot, options Options) *App {
	if cfg == nil {
		if options.ConfigProvider != nil {
			cfg = options.ConfigProvider()
		}
		if cfg == nil {
			cfg = servercfg.Current()
		}
	}
	nodeID := strings.TrimSpace(options.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSpace(cfg.App.Name)
	}
	if nodeID == "" {
		nodeID = "nps"
	}
	services := webservice.New()
	if options.Services != nil {
		services = *options.Services
	}
	if options.ConfigureServices != nil {
		options.ConfigureServices(&services)
	}
	hooks := options.Hooks
	if hooks == nil {
		hooks = NoopHooks{}
	}
	configProvider := options.ConfigProvider
	if configProvider == nil {
		configProvider = servercfg.Current
	}
	return &App{
		NodeID:         nodeID,
		Hooks:          hooks,
		Services:       services,
		ConfigProvider: configProvider,
	}
}

func (a *App) Emit(c Context, event Event) {
	if a == nil || a.Hooks == nil {
		return
	}
	if event.Actor == nil {
		event.Actor = c.Actor()
	}
	if event.Metadata == (RequestMetadata{}) {
		event.Metadata = c.Metadata()
	}
	_ = a.Hooks.OnManagementEvent(c.BaseContext(), event)
}

func (a *App) currentConfig() *servercfg.Snapshot {
	if a != nil && a.ConfigProvider != nil {
		if cfg := a.ConfigProvider(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}

func (a *App) CurrentConfig() *servercfg.Snapshot {
	return a.currentConfig()
}

func (a *App) loginPolicy() webservice.LoginPolicyService {
	if a != nil && a.Services.LoginPolicy != nil {
		return a.Services.LoginPolicy
	}
	return webservice.SharedLoginPolicy()
}

func (a *App) system() webservice.SystemService {
	if a != nil && a.Services.System != nil {
		return a.Services.System
	}
	return webservice.DefaultSystemService{}
}

func (a *App) managementShellAssets(cfg *servercfg.Snapshot) ui.ManagementShellAssets {
	if cfg == nil {
		cfg = a.currentConfig()
	}
	return ui.DefaultManagementShellAssets(cfg.Web.BaseURL)
}

func (a *App) managementShellMetadata(cfg *servercfg.Snapshot) ui.ManagementShellMetadata {
	if cfg == nil {
		cfg = a.currentConfig()
	}
	return ui.ManagementShellMetadata{
		BaseURL:        cfg.Web.BaseURL,
		HeadCustomCode: cfg.Web.HeadCustomCode,
	}
}
