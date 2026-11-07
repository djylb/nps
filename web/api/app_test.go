package api

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

func TestNewWithOptionsUsesOverrides(t *testing.T) {
	providedCfg := &servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "cfg-node"},
	}
	customServices := webservice.New(file.New(t.TempDir()), nil)
	configured := false

	app := NewWithOptions(nil, Options{
		NodeID:   "custom-node",
		Services: &customServices,
		ConfigureServices: func(services *webservice.Services) {
			configured = true
		},
		ConfigProvider: func() *servercfg.Snapshot { return providedCfg },
	})

	if app.NodeID != "custom-node" {
		t.Fatalf("NodeID = %q, want custom-node", app.NodeID)
	}
	if app.CurrentConfig() != providedCfg {
		t.Fatal("CurrentConfig() did not use custom config provider")
	}
	if !configured {
		t.Fatal("ConfigureServices was not called")
	}
	if app.Services.LoginPolicy != customServices.LoginPolicy {
		t.Fatal("Services override was not applied")
	}
}
