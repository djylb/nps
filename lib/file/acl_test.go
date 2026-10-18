package file

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/rate"
)

func writeConf(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, "conf", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readConf(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loadAll(dir string) *JsonDb {
	db := NewJsonDb(dir)
	db.LoadClientFromJsonFile()
	db.LoadTaskFromJsonFile()
	db.LoadHostFromJsonFile()
	db.LoadGlobalFromJsonFile()
	return db
}

func tcpAddr(s string) net.Addr {
	a, _ := net.ResolveTCPAddr("tcp", s)
	return a
}

// Lists saved by earlier versions are compiled as they are: blank entries
// are ignored and entries that are no rule are skipped, and the files are
// not rewritten.
func TestLoadUsesLegacyAccessListsAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	clients := writeConf(t, dir, "clients.json", `[
  {"Id":1,"VerifyKey":"k1","Status":true,"Cnf":{},"Flow":{},"BlackIpList":[" 1.2.3.4","","5.6.7.8","bad host","1.2.3.4"]},
  {"Id":2,"VerifyKey":"k2","Status":true,"Cnf":{},"Flow":{},"BlackIpList":null}
]`)
	tasks := writeConf(t, dir, "tasks.json", `[
  {"Id":1,"Port":1080,"Mode":"mixProxy","Client":{"Id":1},"Flow":{},"Target":{},"DestAclMode":1,"DestAclRules":"example.com\n*.a.com\n10.0.0.0/8"}
]`)
	hosts := writeConf(t, dir, "hosts.json", `[
  {"Id":1,"Host":"a.com","Client":{"Id":2},"Flow":{},"Target":{}}
]`)
	global := writeConf(t, dir, "global.json", `{"BlackIpList":["9.9.9.9",""]}`)
	before := map[string]string{}
	for _, p := range []string{clients, tasks, hosts, global} {
		before[p] = readConf(t, p)
	}

	db := loadAll(dir)

	for p, content := range before {
		if readConf(t, p) != content {
			t.Fatalf("%s rewritten on load", p)
		}
	}
	v, _ := db.Clients.Load(1)
	c := v.(*Client)
	for a, allowed := range map[string]bool{"1.2.3.4:1": false, "5.6.7.8:1": false, "8.8.8.8:1": true} {
		if got := acl.Allows(tcpAddr(a), nil, c.SourceACL(), nil); got != allowed {
			t.Errorf("client 1 list, %s: allowed = %v, want %v", a, got, allowed)
		}
	}
	if acl.Allows(tcpAddr("9.9.9.9:1"), db.Global.SourceACL(), nil, nil) {
		t.Fatal("global list not applied")
	}
	v, _ = db.Tasks.Load(1)
	task := v.(*Tunnel)
	// as geomatch reads it: the bare host name is a keyword
	for a, allowed := range map[string]bool{"example.com:443": true, "www.example.com:443": true, "x.a.com:80": true, "a.com:80": false, "10.1.1.1:80": true, "8.8.8.8:53": false} {
		if got := task.AllowsDestination(a); got != allowed {
			t.Errorf("destination %s: allowed = %v, want %v", a, got, allowed)
		}
	}
}

// The file format of the oldest versions, one object per segment, still
// loads.
func TestLoadObsoleteClientFile(t *testing.T) {
	dir := t.TempDir()
	writeConf(t, dir, "clients.json",
		`{"Id":3,"VerifyKey":"k3","Status":true,"Cnf":{},"Flow":{},"BlackIpList":["1.1.1.1","x y"]}`+"\n"+common.ConnDataSeq+
			`{"Id":4,"VerifyKey":"k4","Status":true,"Cnf":{},"Flow":{}}`+"\n"+common.ConnDataSeq)
	db := NewJsonDb(dir)
	db.LoadClientFromJsonFile()
	v, ok := db.Clients.Load(3)
	if !ok {
		t.Fatal("client 3 lost")
	}
	if acl.Allows(tcpAddr("1.1.1.1:1"), nil, v.(*Client).SourceACL(), nil) {
		t.Fatal("list of an obsolete file not applied")
	}
	if _, ok := db.Clients.Load(4); !ok {
		t.Fatal("client 4 lost")
	}
}

func TestNewTunnelByHostUsesHostLists(t *testing.T) {
	h := &Host{Client: &Client{Id: 1}, Flow: &Flow{}, Target: &Target{}}
	h.SetSourceLists(acl.DenyFirst, []string{"1.2.3.4"}, nil, acl.ScopeUser, "host")
	tn := NewTunnelByHost(h, 443)
	if acl.Allows(tcpAddr("1.2.3.4:1"), nil, nil, tn.SourceACL()) {
		t.Fatal("tunnel built for a host ignores the host lists")
	}
}

func TestGlobalDenied(t *testing.T) {
	db := &DbUtils{JsonDb: NewJsonDb(t.TempDir())}
	if db.GlobalDenied(tcpAddr("192.0.2.1:1"), "Bridge") || db.GlobalDeniedString("192.0.2.1", "Web") {
		t.Fatal("no global settings denied an address")
	}
	g := new(Glob)
	g.SetSourceLists(acl.DenyFirst, []string{"192.0.2.0/24"}, nil, acl.ScopeGlobal, "global")
	db.JsonDb.Global = g
	if !db.GlobalDenied(tcpAddr("192.0.2.1:1"), "Bridge") || !db.GlobalDeniedString("192.0.2.9", "Web") {
		t.Fatal("global blacklist not applied")
	}
	if db.GlobalDenied(tcpAddr("198.51.100.1:1"), "Bridge") {
		t.Fatal("address off the blacklist denied")
	}
}

func TestGlobalTrafficPersisted(t *testing.T) {
	t.Cleanup(func() { rate.ResetGlobalTraffic(); rate.SetGlobalLimit(0) })
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := NewJsonDb(dir)
	db.Global = &Glob{RateLimit: 50}
	rate.SetGlobalTraffic(1234, 5678)
	if err := db.StoreGlobalToJsonFile(); err != nil {
		t.Fatal(err)
	}
	rate.ResetGlobalTraffic()
	loaded := NewJsonDb(dir)
	loaded.LoadGlobalFromJsonFile()
	if in, out := rate.GlobalTraffic(); in != 1234 || out != 5678 {
		t.Fatalf("restored traffic = %d/%d, want 1234/5678", in, out)
	}
	if loaded.Global.RateLimit != 50 || rate.GlobalLimit() != 50*1024 {
		t.Fatalf("restored limit = %d, applied %d", loaded.Global.RateLimit, rate.GlobalLimit())
	}

	empty := NewJsonDb(t.TempDir())
	empty.LoadGlobalFromJsonFile()
	if empty.Global == nil {
		t.Fatal("no global settings left Global nil, so the traffic would not be stored")
	}
}

func TestNullGlobalSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conf", "global.json"), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := NewJsonDb(dir)
	db.LoadGlobalFromJsonFile()
	if db.Global == nil {
		t.Fatal("null global settings left Global nil")
	}
	if err := db.StoreGlobalToJsonFile(); err != nil {
		t.Fatal(err)
	}
}
