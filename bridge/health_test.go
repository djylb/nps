package bridge

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// healthEnv stores a client with one entry that has checks over two targets.
func healthEnv(t *testing.T) (*file.Client, *file.Tunnel) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := file.GetDb() // loads it once, so that nothing loads over this test
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	t.Cleanup(func() { file.Db = old })
	client := &file.Client{Id: 5, Cnf: &file.Config{}, Flow: &file.Flow{}}
	file.GetDb().JsonDb.Clients.Store(client.Id, client)
	tn := &file.Tunnel{Id: 9, Mode: "tcp", Status: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:2"}}
	tn.SetHealthCheck(file.HealthCheck{Enabled: true})
	if err := file.GetDb().NewTask(tn); err != nil {
		t.Fatal(err)
	}
	return client, tn
}

// runSignal serves a signal connection of a node with the given npc version
// and returns the npc's end.
func runSignal(t *testing.T, version string) (net.Conn, func()) {
	t.Helper()
	npc, srv := net.Pipe()
	s := NewTunnel(nil, 30)
	node := NewNode("uuid-"+version, version, legacy.LatestIndex)
	client := NewClient(5, node)
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
	_, tn := healthEnv(t)
	npc, stop := runSignal(t, "0.34.10")
	defer stop()

	_ = npc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := npc.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("old npc got bytes: n=%d err=%v", n, err)
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("read: %v", err)
	}
	_ = npc.SetReadDeadline(time.Time{})

	// Its own reports, from npc.conf, still take effect.
	_ = npc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := legacy.WriteHealth(npc, "127.0.0.1:2", "0"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		only := true
		for range 4 {
			_ = tn.Target.Dial(func(addr string) (bool, error) {
				only = only && addr == "127.0.0.1:1"
				return false, nil
			})
		}
		if only {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("report of an old npc ignored")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHealthGroupsSentToNewClient(t *testing.T) {
	healthEnv(t)
	npc, stop := runSignal(t, "0.35.0")
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
	client, tn := healthEnv(t)
	node := NewNode("n1", "0.35.0", legacy.LatestIndex)
	c := NewClient(client.Id, node)
	tn.NoStore = true
	tn.Routes = file.NewRouteSet(func(string) bool { return true })
	own := &file.Target{TargetStr: "127.0.0.1:3\n127.0.0.1:4"}
	tn.Routes.Publish(node.UUID, own)
	tn.Update(&file.Tunnel{Mode: "tcp", Target: own})
	g, ok := desiredHealthGroups(client.Id, node, c)["t9"]
	if !ok || g.target != own {
		t.Fatalf("group %+v, %v", g, ok)
	}
	other := NewNode("n2", "0.35.0", legacy.LatestIndex)
	if _, ok := desiredHealthGroups(client.Id, other, c)["t9"]; ok {
		t.Fatal("a node that does not publish the entry checks it")
	}
}
