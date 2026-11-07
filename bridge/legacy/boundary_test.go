package legacy

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const packagePath = "github.com/djylb/nps/bridge/legacy"

// allowedImporters lists the only code, tests aside, that may use the legacy
// protocol directly: the bridge and the client that speak it, the commands
// that wire them, and the port multiplexer that tells it from HTTP. Everything
// else goes through package bridge, so that dropping old clients stays local.
var allowedImporters = []string{
	"bridge/",
	"client/",
	"cmd/",
	"server/connection/portmux.go",
}

func TestImportBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if imp == packagePath && !allowed(rel) {
				t.Errorf("%s imports %s; go through package bridge", rel, imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func allowed(rel string) bool {
	for _, p := range allowedImporters {
		if rel == p || strings.HasSuffix(p, "/") && strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}
