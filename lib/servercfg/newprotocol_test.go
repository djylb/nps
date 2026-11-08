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
