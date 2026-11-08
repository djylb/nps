package servercfg

import (
	"slices"
	"testing"
)

func TestNewProtocolWSPathsAndFallbackFile(t *testing.T) {
	s := buildSnapshot(map[string]any{})
	if len(s.Network.NewProtocolWSPaths) != 0 || s.Proxy.Fallback404File != "" {
		t.Fatalf("defaults: %v %q", s.Network.NewProtocolWSPaths, s.Proxy.Fallback404File)
	}
	s = buildSnapshot(map[string]any{"new_protocol_ws_paths": " /a, /b/c ,,", "fallback_404_file": "conf/404.html"})
	if want := []string{"/a", "/b/c"}; !slices.Equal(s.Network.NewProtocolWSPaths, want) {
		t.Fatalf("paths %v, want %v", s.Network.NewProtocolWSPaths, want)
	}
	if s.Proxy.Fallback404File != "conf/404.html" {
		t.Fatal(s.Proxy.Fallback404File)
	}
}

func TestGenericBridgePortRule(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]any
		generic bool
		tcp     bool
	}{
		{"only bridge_port", map[string]any{"bridge_port": "8024"}, true, true},
		{"ignores bridge_type", map[string]any{"bridge_port": "8024", "bridge_type": "kcp"}, true, true},
		{"tls port set", map[string]any{"bridge_port": "8024", "bridge_tls_port": "8025"}, false, true},
		{"old tls key", map[string]any{"bridge_port": "8024", "tls_bridge_port": "8025"}, false, true},
		{"tcp port set", map[string]any{"bridge_port": "8024", "bridge_tcp_port": "8024"}, false, true},
		{"kcp type keeps tcp off without the rule", map[string]any{"bridge_tcp_port": "8024", "bridge_type": "kcp"}, false, false},
		{"shared with http", map[string]any{"bridge_port": "8024", "http_proxy_port": "8024"}, false, true},
		{"no port", map[string]any{}, false, false},
	}
	for _, c := range cases {
		s := buildSnapshot(c.values)
		if s.Network.BridgeGeneric != c.generic || s.Bridge.ServerTCPEnabled != c.tcp {
			t.Errorf("%s: generic %v tcp %v", c.name, s.Network.BridgeGeneric, s.Bridge.ServerTCPEnabled)
		}
	}
}

func TestMixDefaults(t *testing.T) {
	s := buildSnapshot(nil)
	if s.Network.MixPort != 0 || s.Network.MixPlain || !s.Network.MixLegacy || s.Network.ClientMaxPipes != 0 || s.Runtime.DeadS != 0 {
		t.Fatalf("%+v", s.Network)
	}
	if s = buildSnapshot(map[string]any{"disconnect_timeout": "7"}); s.Runtime.DeadS != 7 {
		t.Fatalf("dead_s %d", s.Runtime.DeadS)
	}
}
