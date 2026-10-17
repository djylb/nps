package servercfg

import (
	"os"
	"path/filepath"
	"testing"
)

func resetTestState(t *testing.T) {
	t.Helper()
	mu.Lock()
	appConfig = nil
	appConfigPath = ""
	preferredPath = ""
	mu.Unlock()
	currentSnapshot.Store(defaultSnapshot)
}

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadINIValues(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", "name=nps\nopen=true\nport=8080\nlarge=1234567890123\nempty=\nweb-base.url=/nps\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
	if got := String("name"); got != "nps" {
		t.Fatalf("String(name) = %q, want nps", got)
	}
	if got := String("web_base_url"); got != "/nps" {
		t.Fatalf("String(web_base_url) = %q, want /nps", got)
	}
	if got := String("web.base-url"); got != "/nps" {
		t.Fatalf("String(web.base-url) = %q, want /nps", got)
	}
	if got := String("web-base_url"); got != "/nps" {
		t.Fatalf("String(web-base_url) = %q, want /nps", got)
	}
	cfg := Current()
	if cfg.Web.BaseURL != "/nps" {
		t.Fatalf("Current().Web.BaseURL = %q, want /nps", cfg.Web.BaseURL)
	}
	if got := String("missing"); got != "" {
		t.Fatalf("String(missing) = %q, want empty", got)
	}
	if got := DefaultString("empty", "fallback"); got != "fallback" {
		t.Fatalf("DefaultString(empty) = %q, want fallback", got)
	}
	if got := DefaultString("missing", "fallback"); got != "fallback" {
		t.Fatalf("DefaultString(missing) = %q, want fallback", got)
	}

	open, err := Bool("open")
	if err != nil || !open {
		t.Fatalf("Bool(open) = %v, %v, want true, nil", open, err)
	}

	port, err := Int("port")
	if err != nil || port != 8080 {
		t.Fatalf("Int(port) = %v, %v, want 8080, nil", port, err)
	}

	large, err := Int64("large")
	if err != nil || large != 1234567890123 {
		t.Fatalf("Int64(large) = %v, %v, want 1234567890123, nil", large, err)
	}
}

func TestLoadYAMLNestedValues(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.yaml", "web:\n  port: 8081\n  open-ssl: false\n  base-url: /nps\np2p:\n  stun_servers:\n    - stun1.example.com:3478\n    - stun2.example.com:3478\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	port, err := Int("web_port")
	if err != nil || port != 8081 {
		t.Fatalf("Int(web_port) = %v, %v, want 8081, nil", port, err)
	}
	openSSL, err := Bool("web.open_ssl")
	if err != nil || openSSL {
		t.Fatalf("Bool(web.open_ssl) = %v, %v, want false, nil", openSSL, err)
	}
	if got := String("web-base_url"); got != "/nps" {
		t.Fatalf("String(web-base_url) = %q, want /nps", got)
	}
	if got := String("p2p_stun_servers"); got != "stun1.example.com:3478,stun2.example.com:3478" {
		t.Fatalf("String(p2p_stun_servers) = %q", got)
	}
	cfg := Current()
	if cfg.Network.WebPort != 8081 {
		t.Fatalf("Current().Network.WebPort = %d, want 8081", cfg.Network.WebPort)
	}
	if cfg.P2P.STUNServers != "stun1.example.com:3478,stun2.example.com:3478" {
		t.Fatalf("Current().P2P.STUNServers = %q", cfg.P2P.STUNServers)
	}
}

func TestLoadJSONNestedValues(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.json", "{\n  \"log\": {\"level\": \"debug\"},\n  \"bridge\": {\"tcp-port\": 8024},\n  \"system-info\": {\"display\": true}\n}\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := String("log_level"); got != "debug" {
		t.Fatalf("String(log_level) = %q, want debug", got)
	}
	port, err := Int("bridge.tcp_port")
	if err != nil || port != 8024 {
		t.Fatalf("Int(bridge.tcp_port) = %v, %v, want 8024, nil", port, err)
	}
	infoDisplay, err := Bool("system-info.display")
	if err != nil || !infoDisplay {
		t.Fatalf("Bool(system-info.display) = %v, %v, want true, nil", infoDisplay, err)
	}
}

func TestReload(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", "name=before\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := String("name"); got != "before" {
		t.Fatalf("String(name) = %q, want before", got)
	}

	if err := os.WriteFile(path, []byte("name=after\n"), 0o600); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	if err := Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if got := String("name"); got != "after" {
		t.Fatalf("String(name) = %q, want after", got)
	}
}

func TestDefaultPathsPreferExplicitFile(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.yaml", "web:\n  port: 8081\n")
	SetPreferredPath(path)

	paths := DefaultPaths()
	if len(paths) == 0 || paths[0] != path {
		t.Fatalf("DefaultPaths()[0] = %q, want %q", paths[0], path)
	}
}

func TestIsSupportedConfigPath(t *testing.T) {
	if !IsSupportedConfigPath("nps.conf") {
		t.Fatalf("nps.conf should be supported")
	}
	if !IsSupportedConfigPath("nps.yaml") {
		t.Fatalf("nps.yaml should be supported")
	}
	if !IsSupportedConfigPath("nps.json") {
		t.Fatalf("nps.json should be supported")
	}
	if IsSupportedConfigPath("nps.toml") {
		t.Fatalf("nps.toml should not be supported yet")
	}
}

func TestLoadP2PSettingsIgnoresRemovedKeys(t *testing.T) {
	resetTestState(t)
	path := writeConfig(t, "nps.conf", "p2p_port=6000\np2p_ip=nps.example.com,::\n"+
		"p2p_probe_extra_reply=true\np2p_force_predict_on_restricted=true\np2p_enable_target_spray=true\n"+
		"p2p_target_spray_span=64\np2p_birthday_listen_ports=8\np2p_enable_birthday_attack=true\n"+
		"p2p_enable_pcp_portmap=true\np2p_portmap_lease_seconds=600\np2p_probe_timeout_ms=3000\n")
	if err := Load(path); err != nil {
		t.Fatalf("Load() with removed P2P keys = %v", err)
	}
	cfg := Current()
	if cfg.Network.P2PPort != 6000 || cfg.Network.P2PIP != "nps.example.com,::" {
		t.Fatalf("network P2P settings = %d %q", cfg.Network.P2PPort, cfg.Network.P2PIP)
	}
	p := cfg.P2P
	if p.EnableUPNPPortmap || !p.EnablePCPPortmap || p.EnableNATPMPPortmap || p.PortmapLeaseSeconds != 600 || p.ProbeTimeoutMs != 3000 {
		t.Fatalf("P2P settings = %+v", p)
	}
}

func TestP2PDefaults(t *testing.T) {
	resetTestState(t)
	cfg := Current()
	if cfg.Network.P2PIP != "" || cfg.P2P.EnableUPNPPortmap || cfg.P2P.EnablePCPPortmap || cfg.P2P.EnableNATPMPPortmap || cfg.P2P.PortmapLeaseSeconds != 3600 {
		t.Fatalf("defaults: p2p_ip %q, %+v", cfg.Network.P2PIP, cfg.P2P)
	}
}
