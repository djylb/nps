package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/djylb/nps/"

// rules keep the shared code apart from what only nps runs: the store stays
// with the server, so that npc carries neither, and the entities here stay
// free of both. The bridge and the host proxy reach the store only through
// the interfaces the server passes them.
var rules = []struct {
	dirs   []string // code under these, tests aside
	except []string
	deny   string // may not import this package or those under it
}{
	{[]string{"lib/", "client/", "cmd/npc/", "bridge/legacy/"}, []string{"lib/file/"}, "lib/file"},
	{[]string{"lib/", "client/", "cmd/npc/", "bridge/legacy/"}, nil, "server"},
	{[]string{"bridge/", "server/httpproxy/"}, nil, "lib/file"},
}

func TestImportBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" {
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
			for _, r := range rules {
				if under(rel, r.dirs) && !under(rel, r.except) && (imp == module+r.deny || strings.HasPrefix(imp, module+r.deny+"/")) {
					t.Errorf("%s imports %s", rel, imp)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func under(rel string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(rel, d) {
			return true
		}
	}
	return false
}

// TestStoreHasNoPackageState keeps every store state in its instance: nps
// builds the store and passes it on, so that no package variable of lib/file
// holds entries, indexes or locks shared by all stores.
func TestStoreHasNoPackageState(t *testing.T) {
	dir := filepath.Join("..", "file")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.VAR {
				t.Errorf("lib/file/%s declares a package variable", name)
			}
		}
	}
}
