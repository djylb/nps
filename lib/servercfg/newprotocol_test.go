package servercfg

import (
	"slices"
	"strings"
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
		{"tls type is tcp class", map[string]any{"bridge_port": "8024", "bridge_type": "tls"}, true, true},
		{"kcp type shares the UDP port", map[string]any{"bridge_port": "8024", "bridge_type": "kcp"}, false, false},
		{"udp type is quic only", map[string]any{"bridge_port": "8024", "bridge_type": "udp"}, false, false},
		{"quic type is quic only", map[string]any{"bridge_port": "8024", "bridge_type": "quic"}, false, false},
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

func TestGenericBridgePortUDPClass(t *testing.T) {
	for _, typ := range []string{"kcp", "udp", "quic"} {
		s := buildSnapshot(map[string]any{"bridge_port": "8024", "bridge_type": typ})
		shared := typ == "kcp"
		if s.Network.BridgeQUICPort != 8024 || !s.Bridge.ServerQUICEnabled || s.Bridge.ServerKCPEnabled != shared || s.Network.BridgeKCPQUIC != shared ||
			s.Bridge.ServerTCPEnabled || s.Bridge.ServerTLSEnabled || s.Bridge.ServerWSEnabled {
			t.Errorf("%s: %+v", typ, s.Bridge)
		}
		warned := slices.ContainsFunc(s.Legacy, func(w string) bool { return strings.Contains(w, "QUIC only") })
		if warned != (typ == "udp") {
			t.Errorf("%s: warnings %v", typ, s.Legacy)
		}
	}
	// ports written one by one stay as they are
	s := buildSnapshot(map[string]any{"bridge_port": "8024", "bridge_type": "kcp", "bridge_kcp_port": "8024"})
	if s.Network.BridgeQUICPort != 0 || !s.Bridge.ServerKCPEnabled || s.Network.BridgeKCPQUIC {
		t.Errorf("explicit kcp port: %+v", s.Bridge)
	}
	s = buildSnapshot(map[string]any{"bridge_port": "8024"})
	if len(s.Legacy) != 0 {
		t.Errorf("warned: %v", s.Legacy)
	}
}

func TestCheckQUICALPN(t *testing.T) {
	n := buildSnapshot(nil).Network
	if !slices.Equal(n.NewProtocolQUICALPN, []string{"h3"}) || !slices.Equal(n.QUICALPNList, []string{"nps"}) {
		t.Fatalf("defaults %v %v", n.NewProtocolQUICALPN, n.QUICALPNList)
	}
	if err := n.CheckQUICALPN(true); err == nil {
		t.Error("h3 new protocol ALPN with HTTP/3 on the port")
	}
	n.NewProtocolQUICALPN = []string{"x"}
	if err := n.CheckQUICALPN(true); err != nil {
		t.Error(err)
	}
	n.QUICALPNList = []string{"h3"}
	if err := n.CheckQUICALPN(true); err == nil {
		t.Error("h3 old protocol ALPN with HTTP/3 on the port")
	}
	n.QUICALPNList = []string{"x", "y"}
	if err := n.CheckQUICALPN(false); err == nil {
		t.Error("overlap")
	}
}
