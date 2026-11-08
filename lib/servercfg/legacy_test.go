package servercfg

import (
	"strings"
	"testing"
)

func TestLegacyRenamedKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]any
		port   int
		warn   bool
	}{
		{"old name", map[string]any{"tls_bridge_port": "8025"}, 8025, true},
		{"new name wins", map[string]any{"tls_bridge_port": "8025", "bridge_tls_port": "9025"}, 9025, true},
		{"new name", map[string]any{"bridge_tls_port": "9025"}, 9025, false},
	} {
		s := buildSnapshot(tc.values)
		if s.Network.BridgeTLSPort != tc.port {
			t.Errorf("%s: port %d, want %d", tc.name, s.Network.BridgeTLSPort, tc.port)
		}
		if got := len(s.Legacy) > 0; got != tc.warn {
			t.Errorf("%s: warnings %q", tc.name, s.Legacy)
		}
	}
}

func TestLegacyRemovedKey(t *testing.T) {
	s := buildSnapshot(map[string]any{"ip_limit": "true"})
	if len(s.Legacy) != 1 || !strings.Contains(s.Legacy[0], "registered_only") {
		t.Fatalf("warnings %q", s.Legacy)
	}
	if len(buildSnapshot(map[string]any{}).Legacy) != 0 {
		t.Fatal("warnings without legacy keys")
	}
}

func TestLegacyKeysKnown(t *testing.T) {
	// A renamed key stays a key nps knows, so a reload sees it change.
	keys := Keys()
	for _, l := range legacyKeys {
		if l.current == "" {
			continue
		}
		for _, k := range []string{l.old, l.current} {
			found := false
			for _, key := range keys {
				found = found || key == k
			}
			if !found {
				t.Errorf("%q is not in Keys", k)
			}
		}
	}
}

// allow_secret_link=true lifts the limit of secret tunnels without a
// destination list, with a warning; false is the default and silent.
func TestLegacySecretLink(t *testing.T) {
	s := buildSnapshot(map[string]any{"allow_secret_link": "true"})
	if !s.Feature.LegacySecretLink || len(s.Legacy) != 1 || !strings.Contains(s.Legacy[0], "destination list") {
		t.Fatalf("true: %t, warnings %q", s.Feature.LegacySecretLink, s.Legacy)
	}
	s = buildSnapshot(map[string]any{"allow_secret_link": "false"})
	if s.Feature.LegacySecretLink || len(s.Legacy) != 0 {
		t.Fatalf("false: %t, warnings %q", s.Feature.LegacySecretLink, s.Legacy)
	}
}

func TestLegacyQUICALPNOverlap(t *testing.T) {
	s := buildSnapshot(map[string]any{"quic_alpn": "nps,h3"})
	if got := strings.Join(s.Network.QUICALPNList, ","); got != "nps" {
		t.Fatalf("old list %q", got)
	}
	if len(s.Legacy) != 1 || !strings.Contains(s.Legacy[0], "h3") {
		t.Fatalf("warnings %q", s.Legacy)
	}
	if err := s.Network.CheckQUICALPN(true, false); err != nil {
		t.Fatal(err)
	}
	s = buildSnapshot(map[string]any{"quic_alpn": "h3"})
	if got := strings.Join(s.Network.QUICALPNList, ","); got != "nps" || s.Network.QUICALPN != "nps" {
		t.Fatalf("old list %q", got)
	}
	// a written new_protocol_quic_alpn is the administrator's choice
	s = buildSnapshot(map[string]any{"quic_alpn": "h3", "new_protocol_quic_alpn": "h3"})
	if s.Network.CheckQUICALPN(true, false) == nil {
		t.Fatal("overlap with a written list must stay an error")
	}
	// HTTP/3 on the mixed port stays an error
	s = buildSnapshot(map[string]any{"quic_alpn": "h3", "mix_port": "8024", "https_proxy_port": "8024"})
	if s.Network.CheckQUICALPN(true, s.Network.MixHTTP3()) == nil {
		t.Fatal("h3 with mixed HTTP/3 must be an error")
	}
}
