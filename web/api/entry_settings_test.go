package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"
)

// TestEntrySettingsInFormsAndDocs fails for a form setting of a tunnel or
// host that the add and edit pages do not offer or the API reference does
// not describe.
func TestEntrySettingsInFormsAndDocs(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	apiDoc := read("docs/reference/web-api.md")
	for _, c := range []struct {
		settings []model.EntrySetting
		pages    []string
	}{
		{model.TunnelSettings, []string{"web/views/index/add.html", "web/views/index/edit.html"}},
		{model.HostSettings, []string{"web/views/index/hadd.html", "web/views/index/hedit.html"}},
	} {
		for _, page := range c.pages {
			html := read(page)
			for _, s := range c.settings {
				if s.Form != "" && !strings.Contains(html, `name="`+s.Form+`"`) {
					t.Errorf("%s has no field %q", page, s.Form)
				}
			}
		}
		for _, s := range c.settings {
			if s.Form != "" && !regexp.MustCompile(`\b`+regexp.QuoteMeta(s.Form)+`\b`).MatchString(apiDoc) {
				t.Errorf("docs/reference/web-api.md does not describe %q", s.Form)
			}
		}
	}
}
