package tunnelwire

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/djylb/nps/internal/tunnelwire"

// leakcheckPath is the one package outside the standard library that tests
// here may use; it uses the standard library only.
const leakcheckPath = "github.com/djylb/lifecycle/lifecycletest"

// allowedImporters lists the only code outside this package that may use the
// legacy protocol, so that dropping old clients stays a matter of deleting it.
var allowedImporters = []string{
	"bridge/legacy/",
	"lib/crypt/crypt.go",
	"lib/version/version.go",
	"client/legacy/wire_golden_test.go",
}

func TestImportBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
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
		if !strings.HasSuffix(path, ".go") {
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
		inside := strings.HasPrefix(rel, "internal/tunnelwire/")
		for _, spec := range file.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if inside {
				first, _, _ := strings.Cut(imp, "/")
				if strings.Contains(first, ".") && !strings.HasPrefix(imp, modulePath) && !(strings.HasSuffix(rel, "_test.go") && imp == leakcheckPath) {
					t.Errorf("%s imports %s; this package uses the standard library only", rel, imp)
				}
			} else if strings.HasPrefix(imp, modulePath) && !importerAllowed(rel) {
				t.Errorf("%s imports %s; go through bridge/legacy", rel, imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func importerAllowed(rel string) bool {
	for _, allowed := range allowedImporters {
		if strings.HasPrefix(rel, allowed) {
			return true
		}
	}
	return false
}
