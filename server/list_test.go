package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/file"
)

// loadTestData installs a database loaded from the given clients, tasks and
// hosts files, as nps loads them at start.
func loadTestData(t *testing.T, clients, tasks, hosts string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"clients.json": clients, "tasks.json": tasks, "hosts.json": hosts} {
		if err := os.WriteFile(filepath.Join(dir, "conf", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = file.GetDb()
	old, oldBridge := file.Db, Bridge
	db := &file.DbUtils{JsonDb: file.NewJsonDb(dir)}
	file.Db = db
	db.JsonDb.LoadClientFromJsonFile()
	db.JsonDb.LoadTaskFromJsonFile()
	db.JsonDb.LoadHostFromJsonFile()
	Bridge = bridge.NewTunnel(&sync.Map{}, 30)
	t.Cleanup(func() { file.Db, Bridge = old, oldBridge })
}

// Malformed entries, as an edited or damaged file may hold, are skipped or
// completed instead of panicking at start or when listed.
func TestLoadMalformedData(t *testing.T) {
	loadTestData(t,
		`[null, {"Id":1,"VerifyKey":"a"}, {"Id":2,"VerifyKey":"b","Flow":null,"Cnf":null}]`,
		`[null, {"Id":1,"Port":1,"Client":null}, {"Id":2,"Port":2,"Mode":"tcp","Client":{"Id":1},"Flow":null,"Target":null}, {"Id":3,"Mode":"tcp","Client":{"Id":9}}]`,
		`[null, {"Id":1,"Host":"a.test","Client":null}, {"Id":2,"Host":"b.test","Client":{"Id":2},"Flow":null,"Target":null}]`)
	db := file.GetDb()
	for _, id := range []int{1, 2} {
		c, err := db.GetClient(id)
		if err != nil || c.Flow == nil || c.Cnf == nil {
			t.Fatalf("client %d: %+v, %v", id, c, err)
		}
	}
	if _, err := db.GetTask(1); err == nil {
		t.Fatal("tunnel without a client loaded")
	}
	if tk, err := db.GetTask(2); err != nil || tk.Flow == nil || tk.Target == nil {
		t.Fatalf("tunnel 2 not completed: %v", err)
	}
	if _, err := db.GetTask(3); err == nil {
		t.Fatal("tunnel of a missing client loaded")
	}
	if _, err := db.GetHostById(1); err == nil {
		t.Fatal("host without a client loaded")
	}
	if h, err := db.GetHostById(2); err != nil || h.Flow == nil || h.Target == nil {
		t.Fatalf("host 2 not completed: %v", err)
	}
	for _, field := range []string{"InletFlow", "TotalFlow", "FlowRemain", "Flow.FlowLimit", "Flow.TimeLimit", "Client.IsConnect"} {
		GetTunnel(0, 10, "", 0, "", field, "asc")
		GetHostList(0, 10, 0, "", field, "desc")
		GetClientList(0, 10, "", field, "asc", 0)
	}
	dealClientData()
}

// A page of a sorted list is taken from all the matching entries sorted,
// not from a page sorted on its own.
func TestListsSortBeforePaging(t *testing.T) {
	loadTestData(t,
		`[{"Id":1,"VerifyKey":"a","Remark":"c"}, {"Id":2,"VerifyKey":"b","Remark":"a"}, {"Id":3,"VerifyKey":"c","Remark":"d"}, {"Id":4,"VerifyKey":"d","Remark":"b"}]`,
		`[{"Id":1,"Port":1,"Mode":"tcp","Client":{"Id":1},"Remark":"c"}, {"Id":2,"Port":2,"Mode":"tcp","Client":{"Id":1},"Remark":"a"}, {"Id":3,"Port":3,"Mode":"tcp","Client":{"Id":1},"Remark":"d"}]`,
		`[{"Id":1,"Host":"c.test","Client":{"Id":1}}, {"Id":2,"Host":"a.test","Client":{"Id":1}}, {"Id":3,"Host":"d.test","Client":{"Id":1}}, {"Id":4,"Host":"b.test","Client":{"Id":1}}]`)

	hosts, n := GetHostList(0, 2, 0, "", "Host", "asc")
	if n != 4 || len(hosts) != 2 || hosts[0].Host != "a.test" || hosts[1].Host != "b.test" {
		t.Fatalf("hosts page 1 = %v (of %d), want a.test, b.test", hostNames(hosts), n)
	}
	if hosts, _ = GetHostList(2, 2, 0, "", "Host", "asc"); len(hosts) != 2 || hosts[0].Host != "c.test" || hosts[1].Host != "d.test" {
		t.Fatalf("hosts page 2 = %v, want c.test, d.test", hostNames(hosts))
	}
	if hosts, _ = GetHostList(0, 1, 0, "", "Id", "desc"); len(hosts) != 1 || hosts[0].Id != 4 {
		t.Fatalf("highest host id = %v, want 4", hostNames(hosts))
	}

	clients, n := GetClientList(0, 2, "", "Remark", "asc", 0)
	if n != 4 || len(clients) != 2 || clients[0].Remark != "a" || clients[1].Remark != "b" {
		t.Fatalf("clients page 1 = %d clients of %d, want remarks a, b", len(clients), n)
	}
	if clients, _ = GetClientList(2, 5, "", "Remark", "asc", 0); len(clients) != 2 || clients[0].Remark != "c" || clients[1].Remark != "d" {
		t.Fatalf("clients page 2 = %d clients, want remarks c, d", len(clients))
	}

	tunnels, n := GetTunnel(0, 2, "tcp", 0, "", "Remark", "asc")
	if n != 3 || len(tunnels) != 2 || tunnels[0].Remark != "a" || tunnels[1].Remark != "c" {
		t.Fatalf("tunnels page 1 = %d tunnels of %d", len(tunnels), n)
	}
}

func hostNames(list []*file.Host) []string {
	names := make([]string, 0, len(list))
	for _, h := range list {
		names = append(names, h.Host)
	}
	return names
}

func TestListsSortByActualFlow(t *testing.T) {
	loadTestData(t,
		`[{"Id":1,"VerifyKey":"a","Flow":{"InletFlow":5}}, {"Id":2,"VerifyKey":"b","Flow":{"InletFlow":30}}, {"Id":3,"VerifyKey":"c","Flow":{"ExportFlow":20}}]`,
		`[{"Id":1,"Port":1,"Mode":"tcp","Client":{"Id":1},"Flow":{"InletFlow":5}}, {"Id":2,"Port":2,"Mode":"tcp","Client":{"Id":1},"Flow":{"ExportFlow":30}}]`,
		`[{"Id":1,"Host":"a.test","Client":{"Id":1},"Flow":{"InletFlow":1}}, {"Id":2,"Host":"b.test","Client":{"Id":1},"Flow":{"InletFlow":9}}]`)
	if clients, _ := GetClientList(0, 0, "", "ActualFlow", "desc", 0); len(clients) != 3 || clients[0].Id != 2 || clients[1].Id != 3 || clients[2].Id != 1 {
		t.Fatalf("clients by actual flow desc: %d", len(clients))
	}
	if tunnels, _ := GetTunnel(0, 0, "tcp", 0, "", "ActualFlow", "asc"); len(tunnels) != 2 || tunnels[0].Id != 1 {
		t.Fatal("tunnels by actual flow asc")
	}
	if hosts, _ := GetHostList(0, 0, 0, "", "ActualFlow", "desc"); len(hosts) != 2 || hosts[0].Host != "b.test" {
		t.Fatalf("hosts by actual flow desc = %v", hostNames(hosts))
	}
}
