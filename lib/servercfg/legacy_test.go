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
