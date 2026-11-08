package main

import (
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/web/routers"
)

func appliers(t *testing.T) []servercfg.Applier {
	db := file.New(t.TempDir())
	srv := server.New(db, server.Config{CoreEntry: coreStub{}})
	t.Cleanup(func() { _ = srv.Close() })
	srv.AddConfigApplier(routers.ConfigApplier(db, nil, srv, func(http.Handler) {}))
	return srv.ConfigAppliers()
}

// TestEveryKeyHasAnApplier fails for a config key that no applier of a
// reload owns, that two own or that nps does not read, so that a new key
// has to say whether a reload applies it or a restart does.
func TestEveryKeyHasAnApplier(t *testing.T) {
	a := appliers(t)
	if err := servercfg.CheckSettings(a); err != nil {
		t.Fatal(err)
	}
	for _, key := range servercfg.Unowned(a) {
		t.Errorf("no applier owns %s: add it to the applier that applies it, or to one that reports a restart", key)
	}
}

// TestOperationsDocMatchesAppliers checks that the operations guide lists
// every config key once, as taking effect at once or after a restart as
// its applier says.
func TestOperationsDocMatchesAppliers(t *testing.T) {
	restart := map[string]bool{}
	for _, a := range appliers(t) {
		r := a.Apply != nil && reflect.ValueOf(a.Apply).Pointer() == reflect.ValueOf(servercfg.RestartRequired).Pointer()
		for _, key := range a.Settings {
			restart[key] = r
		}
	}
	doc, err := os.ReadFile("../../docs/guide/server/operations.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "立即生效：")
	mid := strings.Index(text, "以下变更需要重启才生效")
	end := strings.Index(text, "## 停止和重启")
	if start < 0 || mid < start || end < mid {
		t.Fatal("the lists of the reload section are not found")
	}
	code := regexp.MustCompile("`([a-z0-9_]+)`")
	keys := servercfg.Keys()
	listed := map[string]int{}
	for _, part := range []struct {
		text    string
		restart bool
	}{{text[start:mid], false}, {text[mid:end], true}} {
		for _, m := range code.FindAllStringSubmatch(part.text, -1) {
			key := m[1]
			listed[key]++
			if !slices.Contains(keys, key) {
				t.Errorf("the guide names %s, which is not a config key", key)
				continue
			}
			if restart[key] != part.restart {
				t.Errorf("the guide lists %s with the wrong group: restart is %v", key, restart[key])
			}
		}
	}
	for _, key := range keys {
		if listed[key] == 0 {
			t.Errorf("the guide does not list %s", key)
		}
	}
}
