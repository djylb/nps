package coreapi

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Only the protocol's own packages and the commands that wire them use zmux
// or the wire types; the bridge and the client see this package.
func TestImportBoundary(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	deny := []string{"github.com/zmuxio/", "github.com/klauspost/compress", "github.com/djylb/nps/internal/coreproto/wire", "github.com/djylb/nps/internal/coreproto/session"}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && strings.HasPrefix(name, ".") || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/coreproto/") || strings.HasPrefix(rel, "cmd/") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, s := range f.Imports {
			imp, _ := strconv.Unquote(s.Path.Value)
			for _, p := range deny {
				if strings.HasPrefix(imp, p) {
					t.Errorf("%s imports %s; go through coreapi", rel, imp)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
