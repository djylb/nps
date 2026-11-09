package bridge

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/version"
)

// signalNode returns a node announcing vs whose signal connection is one end
// of a pipe, and the other end.
func signalNode(t *testing.T, uuid, vs string) (*Node, *conn.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	n := NewNode(uuid, vs, legacy.LatestIndex)
	n.AddSignal(conn.NewConn(a))
	return n, conn.NewConn(b)
}

func TestRequestClientUpdate(t *testing.T) {
	b := NewTunnel(file.New(t.TempDir()), &sync.Map{}, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})

	if _, err := b.RequestClientUpdate(1); !errors.Is(err, ErrClientOffline) {
		t.Fatalf("unknown client: err = %v, want ErrClientOffline", err)
	}

	old, _ := signalNode(t, "old", "0.34.8")
	b.Client.Store(1, NewClient(1, old, Primary))
	if _, err := b.RequestClientUpdate(1); !errors.Is(err, ErrUpdateUnsupported) {
		t.Fatalf("old npc only: err = %v, want ErrUpdateUnsupported", err)
	}

	cur, peer := signalNode(t, "new", legacy.RemoteUpdateMinVersion)
	v, _ := b.Client.Load(1)
	v.(*Client).AddNode(cur)
	got := make(chan string, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		flag, err := legacy.ReadFlag(peer)
		if err == nil {
			_, err = legacy.ReadFrame(peer)
		}
		if err != nil {
			flag = err.Error()
		}
		got <- flag
	}()
	n, err := b.RequestClientUpdate(1)
	if err != nil || n != 1 {
		t.Fatalf("RequestClientUpdate = %d, %v; want 1 npc asked", n, err)
	}
	if flag := <-got; flag != legacy.UpdateRequest {
		t.Fatalf("npc read %q, want the update request", flag)
	}
}

// updCore is a core session whose update capability and request are set by
// the test.
type updCore struct {
	fakeCore
	can   bool
	asked int
}

func (u *updCore) CanUpdate() bool                     { return u.can }
func (u *updCore) RequestUpdate(context.Context) error { u.asked++; return nil }

func updNode(uuid, vs string, can bool) (*Node, *updCore) {
	u := &updCore{fakeCore: fakeCore{id: uuid, client: 1}, can: can}
	n := NewNode(uuid, vs, legacy.LatestIndex)
	n.AddCore(u)
	return n, u
}

func bridgeWithLatest(t *testing.T, latest func() string) *Bridge {
	t.Helper()
	return NewTunnel(file.New(t.TempDir()), &sync.Map{}, Options{CoreEntry: stubEntry{}, DisconnectTime: 30, LatestVersion: latest})
}

// A node can be updated when it acts on update requests and runs a version
// older than the latest release; the latest is the server's own version while
// unknown.
func TestNodeCanUpdate(t *testing.T) {
	legacyNew, _ := signalNode(t, "l-new", legacy.RemoteUpdateMinVersion)
	legacyOld, _ := signalNode(t, "l-old", "0.34.8")
	noSignal := NewNode("l-nosig", legacy.RemoteUpdateMinVersion, legacy.LatestIndex)
	coreOn, _ := updNode("c-on", "1.2.0", true)
	coreOff, _ := updNode("c-off", "1.2.0", false)
	coreNew, _ := updNode("c-new", "1.5.0", true)

	for _, c := range []struct {
		name   string
		node   *Node
		latest string
		want   bool
	}{
		{"legacy below latest", legacyNew, "0.35.0", true},
		{"legacy equal latest", legacyNew, legacy.RemoteUpdateMinVersion, false},
		{"legacy above latest", legacyNew, "0.34.0", false},
		{"legacy without the capability", legacyOld, "9.0.0", false},
		{"legacy without signal", noSignal, "9.0.0", false},
		{"core below latest", coreOn, "1.3.0", true},
		{"core equal latest", coreOn, "1.2.0", false},
		{"core above latest", coreNew, "1.3.0", false},
		{"core without the capability", coreOff, "9.0.0", false},
	} {
		if got := c.node.canUpdate(c.latest); got != c.want {
			t.Errorf("%s: canUpdate(%s) = %v, want %v", c.name, c.latest, got, c.want)
		}
	}
}

func TestLatestVersionFallback(t *testing.T) {
	if got := bridgeWithLatest(t, nil).latestVersion(); got != version.VERSION {
		t.Fatalf("no source: %q, want the server's version", got)
	}
	if got := bridgeWithLatest(t, func() string { return "" }).latestVersion(); got != version.VERSION {
		t.Fatalf("unknown latest: %q, want the server's version", got)
	}
	if got := bridgeWithLatest(t, func() string { return "7.0.0" }).latestVersion(); got != "7.0.0" {
		t.Fatalf("known latest: %q", got)
	}

	// The decision follows the fallback: a node older than nps itself.
	n, _ := updNode("c", "0.0.1", true)
	b := bridgeWithLatest(t, func() string { return "" })
	b.Client.Store(1, NewClient(1, n, Primary))
	if !b.ClientCanUpdate(1) {
		t.Fatal("node older than the server cannot update while the latest is unknown")
	}
	n2, _ := updNode("c2", version.VERSION, true)
	b.Client.Store(2, NewClient(2, n2, Primary))
	if b.ClientCanUpdate(2) {
		t.Fatal("node at the server's version can update while the latest is unknown")
	}
}

// ClientCanUpdate is true when any node can; RequestClientUpdate asks only
// those, and reports ErrUpdateUnsupported when none can.
func TestRequestClientUpdateOnlyUpdatable(t *testing.T) {
	b := bridgeWithLatest(t, func() string { return "1.3.0" })
	if b.ClientCanUpdate(1) {
		t.Fatal("unknown client can update")
	}

	cur, curCore := updNode("cur", "1.3.0", true)  // at the latest
	off, offCore := updNode("off", "1.0.0", false) // cannot act on it
	b.Client.Store(1, NewClient(1, cur, Primary))
	c1, _ := b.Client.Load(1)
	c1.(*Client).AddNode(off)
	if b.ClientCanUpdate(1) {
		t.Fatal("no node can update, yet ClientCanUpdate")
	}
	if _, err := b.RequestClientUpdate(1); !errors.Is(err, ErrUpdateUnsupported) {
		t.Fatalf("err = %v, want ErrUpdateUnsupported", err)
	}
	if curCore.asked+offCore.asked != 0 {
		t.Fatal("a node that cannot be updated was asked")
	}

	old, oldCore := updNode("old", "1.0.0", true)
	c1.(*Client).AddNode(old)
	oldLegacy, peer := signalNode(t, "old-legacy", legacy.RemoteUpdateMinVersion)
	c1.(*Client).AddNode(oldLegacy)
	if !b.ClientCanUpdate(1) {
		t.Fatal("ClientCanUpdate = false with updatable nodes")
	}
	got := make(chan string, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		flag, err := legacy.ReadFlag(peer)
		if err == nil {
			_, err = legacy.ReadFrame(peer)
		}
		if err != nil {
			flag = err.Error()
		}
		got <- flag
	}()
	n, err := b.RequestClientUpdate(1)
	if err != nil || n != 2 {
		t.Fatalf("RequestClientUpdate = %d, %v; want the 2 updatable nodes", n, err)
	}
	if oldCore.asked != 1 || curCore.asked != 0 || offCore.asked != 0 {
		t.Fatalf("asked: old %d, current %d, off %d", oldCore.asked, curCore.asked, offCore.asked)
	}
	if flag := <-got; flag != legacy.UpdateRequest {
		t.Fatalf("legacy npc read %q", flag)
	}
}

// shapedCore reports fixed shaping statistics.
type shapedCore struct {
	fakeCore
	st coreapi.ShapingStats
}

func (s *shapedCore) ShapingStats() coreapi.ShapingStats { return s.st }

// The shaping statistics of a client are the sum over its core sessions.
func TestClientShapingStats(t *testing.T) {
	b := bridgeWithLatest(t, func() string { return "" })
	if _, ok := b.ClientShapingStats(1); ok {
		t.Fatal("stats of a client that is not connected")
	}
	a := &shapedCore{fakeCore: fakeCore{id: "a", client: 1}, st: coreapi.ShapingStats{Wire: 10, Stall: time.Second, Injected: 2, Faults: 1}}
	c := &shapedCore{fakeCore: fakeCore{id: "b", client: 1}, st: coreapi.ShapingStats{Wire: 5, Stall: time.Second, Injected: 1, InjectDropped: 3}}
	na, nc := NewNode("a", "1", legacy.LatestIndex), NewNode("b", "1", legacy.LatestIndex)
	na.AddCore(a)
	nc.AddCore(c)
	cl := NewClient(1, na, Primary)
	cl.AddNode(nc)
	b.Client.Store(1, cl)
	st, ok := b.ClientShapingStats(1)
	want := coreapi.ShapingStats{Wire: 15, Stall: 2 * time.Second, Injected: 3, InjectDropped: 3, Faults: 1}
	if !ok || st != want {
		t.Fatalf("stats = %+v, %v; want %+v", st, ok, want)
	}
}
