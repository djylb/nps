package bridge

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/healthcheck"
)

// healthEnv stores a client with one entry that has checks over two targets.
func healthEnv(t *testing.T) (*file.DbUtils, *model.Client, *model.Tunnel) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)
	client := &model.Client{Id: 5, Cnf: &model.Config{}, Flow: &model.Flow{}}
	db.JsonDb.Clients.Store(client.Id, client)
	tn := &model.Tunnel{Id: 9, Mode: "tcp", Status: true, Client: client, Target: &model.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:2"}}
	tn.SetHealthCheck(model.HealthCheck{Enabled: true})
	if err := db.NewTask(tn); err != nil {
		t.Fatal(err)
	}
	return db, client, tn
}

// runSignal serves a signal connection of a node with the given npc version
// and returns the npc's end.
func runSignal(t *testing.T, db *file.DbUtils, version string) (net.Conn, func()) {
	t.Helper()
	npc, srv := net.Pipe()
	s := NewTunnel(db, nil, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	node := NewNode("uuid-"+version, version, legacy.LatestIndex)
	client := NewClient(5, node, Primary)
	s.Client.Store(5, client)
	c := conn.NewConn(srv)
	node.AddSignal(c)
	node.AddTunnel(&fakeOpenTunnel{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.GetHealthFromClient(5, c, client, node)
	}()
	return npc, func() {
		_ = npc.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("signal reader did not end")
		}
	}
}

// A released npc takes an unknown message for a broken stream: it must get none.
func TestHealthGroupsNotSentToOldClient(t *testing.T) {
	db, _, tn := healthEnv(t)
	npc, stop := runSignal(t, db, "0.34.10")
	defer stop()

	_ = npc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := npc.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("old npc got bytes: n=%d err=%v", n, err)
	} else if ne, ok := errors.AsType[net.Error](err); !ok || !ne.Timeout() {
		t.Fatalf("read: %v", err)
	}
	_ = npc.SetReadDeadline(time.Time{})

	// Reports of checks it configured itself are ignored, and the connection
	// stays: each write on the pipe returns only once the server read it.
	for range 2 {
		_ = npc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := legacy.WriteGroupHealth(npc, healthcheck.Report{Addr: "127.0.0.1:2"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for range 4 {
		_ = tn.Target.Dial(func(addr string) (bool, error) {
			seen[addr] = true
			return false, nil
		})
	}
	if !seen["127.0.0.1:2"] {
		t.Fatal("report of an old npc applied")
	}
}

func TestHealthGroupsSentToNewClient(t *testing.T) {
	db, _, _ := healthEnv(t)
	npc, stop := runSignal(t, db, "0.35.0")
	defer stop()
	_ = npc.SetReadDeadline(time.Now().Add(5 * time.Second))
	flag, err := legacy.ReadFlag(npc)
	if err != nil || flag != legacy.HealthSet {
		t.Fatalf("flag %q, err %v", flag, err)
	}
	m, err := legacy.ReadHealthSet(npc)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "t9" || m.Type != "tcp" || m.Interval != 10 || m.Timeout != 3 || m.Fall != 3 || len(m.Targets) != 2 {
		t.Fatalf("group %+v", m)
	}
}

// fakeOpenTunnel keeps a node online.
type fakeOpenTunnel struct{ legacy.Carrier }

func (*fakeOpenTunnel) Closed() bool   { return false }
func (*fakeOpenTunnel) Close() error   { return nil }
func (*fakeOpenTunnel) Reason() string { return "" }

// An entry published through npc.conf can be enabled on the server: each
// instance checks its own target, and publishing again keeps the setting.
func TestHealthGroupOfPublishedEntry(t *testing.T) {
	db, client, tn := healthEnv(t)
	node := NewNode("n1", "0.35.0", legacy.LatestIndex)
	c := NewClient(client.Id, node, Primary)
	tn.NoStore = true
	tn.Routes = model.NewRouteSet(func(string) bool { return true })
	own := &model.Target{TargetStr: "127.0.0.1:3\n127.0.0.1:4"}
	tn.Routes.Publish(node.UUID, own)
	tn.Update(&model.Tunnel{Mode: "tcp", Target: own})
	g, ok := desiredHealthGroups(db, client.Id, node, c)["t9"]
	if !ok || g.target != own {
		t.Fatalf("group %+v, %v", g, ok)
	}
	other := NewNode("n2", "0.35.0", legacy.LatestIndex)
	if _, ok := desiredHealthGroups(db, client.Id, other, c)["t9"]; ok {
		t.Fatal("a node that does not publish the entry checks it")
	}
}

// A report that was on its way when its group closed must not take the target
// out for good: nothing would correct it.
func TestLateHealthReportDropped(t *testing.T) {
	target := &model.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:2"}
	p := &healthPush{sent: make(map[string]healthGroup)}
	inRotation := func() int {
		seen := map[string]bool{}
		for range 4 {
			_ = target.Dial(func(addr string) (bool, error) {
				seen[addr] = true
				return false, nil
			})
		}
		return len(seen)
	}
	g := healthGroup{target: target, msg: healthcheck.Group{Name: "t1", Targets: target.Addresses()}}
	p.remember(g)
	down := healthcheck.Report{Group: "t1", Addr: "127.0.0.1:2"}
	p.apply(down)
	if n := inRotation(); n != 1 {
		t.Fatalf("%d in rotation after a report, want 1", n)
	}
	p.forget("t1")
	if n := inRotation(); n != 2 {
		t.Fatalf("%d in rotation after closing, want 2", n)
	}
	p.apply(down)
	if n := inRotation(); n != 2 {
		t.Fatalf("%d in rotation after a late report, want 2", n)
	}
}

func TestUDPTunnelGetsNoHealthGroup(t *testing.T) {
	db, client, tn := healthEnv(t)
	tn.Mode = "udp"
	node := NewNode("n1", "0.35.0", legacy.LatestIndex)
	if got := desiredHealthGroups(db, client.Id, node, NewClient(client.Id, node, Primary)); len(got) != 0 {
		t.Fatalf("groups %v", got)
	}
}

func TestRepublishedTunnelAnnouncesChange(t *testing.T) {
	db, client, tn := healthEnv(t)
	tn.NoStore, tn.Remark, tn.Port = true, "r", 7000
	tn.Routes = model.NewRouteSet(func(string) bool { return true })
	s := NewTunnel(db, nil, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	var calls atomic.Int32
	db.OnEntriesChanged(func() { calls.Add(1) })
	again := &model.Tunnel{Id: tn.Id, Port: tn.Port, Mode: "tcp", Remark: "r", Password: "", Target: &model.Target{TargetStr: "a:1"}, Client: client}
	if _, _, err := s.publishTask(again, client, "n1", legacy.CapsOf(legacy.LatestIndex, "0.35.0")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == 0 {
		t.Fatal("no change announced")
	}
}
