package api

import (
	"context"
	"strings"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

var log = obs.Module("web")

type RequestMetadata struct {
	NodeID    string `json:"node_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Source    string `json:"source,omitempty"`
}

type App struct {
	NodeID         string
	Services       webservice.Services
	ConfigProvider func() *servercfg.Snapshot
	// Identity holds the key the login pages encrypt credentials to.
	Identity *crypt.Identity
}

type Options struct {
	NodeID            string
	Services          *webservice.Services
	ConfigureServices func(*webservice.Services)
	ConfigProvider    func() *servercfg.Snapshot
	// Identity holds the key the login pages encrypt credentials to.
	Identity *crypt.Identity
	// Store is the store the services act on.
	Store *file.DbUtils
	// Runtime is the running server the services act on; nil means none.
	Runtime webservice.Runtime
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
	services := webservice.New(options.Store, options.Runtime)
	if options.Services != nil {
		services = *options.Services
	}
	if options.ConfigureServices != nil {
		options.ConfigureServices(&services)
	}
	configProvider := options.ConfigProvider
	if configProvider == nil {
		configProvider = servercfg.Current
	}
	return &App{
		NodeID:         nodeID,
		Services:       services,
		ConfigProvider: configProvider,
		Identity:       options.Identity,
	}
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

type Handler func(Context)

type Context interface {
	BaseContext() context.Context
	String(string) string
	Int(string, ...int) int
	Bool(string, ...bool) bool
	Method() string
	Host() string
	RemoteAddr() string
	RequestHeader(string) string
	SessionValue(string) interface{}
	// SetSessionValue and DeleteSessionValue change the session and save it
	// in the response; an error says that it could not be saved.
	SetSessionValue(string, interface{}) error
	DeleteSessionValue(string) error
	RespondJSON(int, interface{})
	RespondString(int, string)
	RespondData(int, string, []byte)
	Redirect(int, string)
	SetResponseHeader(string, string)
	IsWritten() bool
	Actor() *Actor
	SetActor(*Actor)
	Metadata() RequestMetadata
}
