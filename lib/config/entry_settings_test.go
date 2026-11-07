package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/djylb/nps/lib/model"
)

// TestEntrySettingsInDocs fails for an npc.conf key of a tunnel or host that
// the configuration reference does not describe.
func TestEntrySettingsInDocs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "npc-config.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slices.Concat(model.TunnelSettings, model.HostSettings) {
		if s.Conf != "" && !regexp.MustCompile(`\b`+regexp.QuoteMeta(s.Conf)+`\b`).Match(b) {
			t.Errorf("docs/reference/npc-config.md does not describe %q", s.Conf)
		}
	}
}
