package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/model"
)

// A stored file tunnel with a port becomes a backend and a tcp tunnel once;
// reloading adds nothing and keeps the identifier.
func TestStoredFileTunnelConverted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := New(root)
	c := model.NewClient("k", false, false)
	c.Status = true
	if err := db.NewClient(c); err != nil {
		t.Fatal(err)
	}
	raw := &model.Tunnel{Id: 7, Port: 18081, Mode: "file", Remark: "f", Client: c, Flow: new(model.Flow), Target: new(model.Target)}
	db.JsonDb.Tasks.Store(raw.Id, raw)
	if err := db.JsonDb.StoreTasksToJsonFile(); err != nil {
		t.Fatal(err)
	}
	load := func() (backend, tcp *model.Tunnel, n int) {
		t.Helper()
		db.JsonDb.Tasks.Clear()
		if err := db.JsonDb.LoadTaskFromJsonFile(); err != nil {
			t.Fatal(err)
		}
		db.JsonDb.Tasks.Range(func(_, v any) bool {
			n++
			switch tl := v.(*model.Tunnel); {
			case tl.IsVirtualBackend():
				backend = tl
			case tl.Mode == "tcp":
				tcp = tl
			}
			return true
		})
		return
	}
	b1, c1, n1 := load()
	if n1 != 2 || b1 == nil || c1 == nil || b1.Port != 0 || c1.Port != 18081 || c1.Id == b1.Id {
		t.Fatalf("after conversion: %d rows, backend %+v, tcp %+v", n1, b1, c1)
	}
	if c1.Target.TargetStr != b1.Target.TargetStr {
		t.Fatalf("targets %q, %q", c1.Target.TargetStr, b1.Target.TargetStr)
	}
	b2, c2, n2 := load()
	if n2 != 2 || b2.Target.TargetStr != b1.Target.TargetStr || c2.Id != c1.Id {
		t.Fatalf("after restart: %d rows, %q, id %d", n2, b2.Target.TargetStr, c2.Id)
	}
}
