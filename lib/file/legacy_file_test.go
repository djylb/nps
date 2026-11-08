package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/model"
)

// A stored file tunnel with a port becomes a tcp tunnel to its service once;
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
	load := func() (tcp *model.Tunnel, n int) {
		t.Helper()
		db.JsonDb.Tasks.Clear()
		if err := db.JsonDb.LoadTaskFromJsonFile(); err != nil {
			t.Fatal(err)
		}
		db.JsonDb.Tasks.Range(func(_, v any) bool {
			n++
			tcp = v.(*model.Tunnel)
			return true
		})
		return
	}
	c1, n1 := load()
	if n1 != 1 || c1.Mode != "tcp" || c1.Port != 18081 || !c1.Generated {
		t.Fatalf("after conversion: %d rows, %+v", n1, c1)
	}
	if _, ok := model.ParseServiceTarget(c1.Target.TargetStr); !ok {
		t.Fatalf("target %q", c1.Target.TargetStr)
	}
	c2, n2 := load()
	if n2 != 1 || c2.Target.TargetStr != c1.Target.TargetStr || c2.Id != c1.Id {
		t.Fatalf("after restart: %d rows, %+v", n2, c2)
	}
}

// A generated tunnel whose port another stored tunnel holds is not made, and
// reloading makes nothing more.
func TestStoredFileTunnelPortTaken(t *testing.T) {
	dir := t.TempDir()
	writeConf(t, dir, "clients.json", `[{"Id":1,"VerifyKey":"k1","Status":true,"Cnf":{},"Flow":{}}]`)
	writeConf(t, dir, "tasks.json", `[
  {"Id":1,"Port":9000,"Mode":"file","Remark":"f","Client":{"Id":1},"Flow":{},"Target":{}},
  {"Id":2,"Port":9000,"Mode":"tcp","Client":{"Id":1},"Flow":{},"Target":{"TargetStr":"127.0.0.1:1"}}
]`)
	writeConf(t, dir, "hosts.json", `[]`)
	writeConf(t, dir, "global.json", `{}`)
	for range 2 {
		db := loadAll(dir)
		n := 0
		db.Tasks.Range(func(_, _ any) bool { n++; return true })
		if v, _ := db.Tasks.Load(2); n != 1 || v == nil {
			t.Fatalf("%d rows", n)
		}
	}
}
