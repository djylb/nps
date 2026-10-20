package rate

import (
	"bytes"
	"io"
	"testing"
)

type groupTestConn struct{ *bytes.Reader }

func (groupTestConn) Write(b []byte) (int, error) { return len(b), nil }
func (groupTestConn) Close() error                { return nil }

func TestGroupsShareAdmissionAndTraffic(t *testing.T) {
	baseline := RootGroup().Conns()
	oldMax := RootGroup().MaxConns()
	RootGroup().SetMaxConns(baseline + 2)
	t.Cleanup(func() { RootGroup().SetMaxConns(oldMax); ResetGlobalTraffic() })
	client := NewRate(0)
	client.Group().SetMaxConns(2)
	a, b := new(Directional), new(Directional)
	ga, gb := a.Group(client.Group()), b.Group(client.Group())
	ga.SetMaxConns(1)
	if err := ga.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := ga.Acquire(); err == nil {
		t.Fatal("leaf cap ignored")
	}
	if err := gb.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := gb.Acquire(); err == nil {
		t.Fatal("parent cap ignored")
	}
	if RootGroup().Conns() != baseline+2 || client.Group().Conns() != 2 || ga.Conns() != 1 || gb.Conns() != 1 {
		t.Fatal("unbalanced ancestor reservation/rollback")
	}
	ga.Release()
	gb.Release()
	if RootGroup().Conns() != baseline {
		t.Fatal("release leaked ancestor slots")
	}
	ResetGlobalTraffic()
	c := NewRateConn(groupTestConn{bytes.NewReader(make([]byte, 17))}, client, a)
	if _, err := c.Write(make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, make([]byte, 17)); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if in, out := GlobalTraffic(); in != 29 || out != 17 {
		t.Fatalf("global double count or reversed directions: %d/%d", in, out)
	}
	download, upload := client.Group().Traffic().Total()
	if upload != 29 || download != 17 {
		t.Fatal("client traffic differs from global")
	}
	download, upload = ga.Traffic().Total()
	if upload != 29 || download != 17 {
		t.Fatal("leaf traffic differs from ancestors")
	}
}

func TestGroupMovesKeepActiveSlots(t *testing.T) {
	first, second := NewRate(0), NewRate(0)
	entry := new(Directional)
	g := entry.Group(first.Group())
	if err := g.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer g.Release()
	entry.Group(second.Group())
	if first.Group().Conns() != 0 || second.Group().Conns() != 1 {
		t.Fatal("changing owner lost or duplicated a live slot")
	}
	second.Group().SetMaxConns(1)
	if err := g.Acquire(); err == nil {
		t.Fatal("new owner's cap not applied")
	}
}

func TestCachedWrapperCannotUndoOwnerMove(t *testing.T) {
	a, b := NewRate(0), NewRate(0)
	entry := new(Directional)
	g := entry.Group(a.Group())
	existing := NewRateConn(groupTestConn{bytes.NewReader(nil)}, a, entry)
	entry.Group(b.Group())
	stale := NewRateConn(groupTestConn{bytes.NewReader(nil)}, a, entry)
	if _, err := existing.Write(make([]byte, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Write(make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	if g.Parent() != b.Group() {
		t.Fatal("stale dial undid accepted owner change")
	}
	_, aBytes := a.Group().Traffic().Total()
	_, bBytes := b.Group().Traffic().Total()
	if aBytes != 0 || bBytes != 8 {
		t.Fatalf("post-move accounting: old=%d new=%d", aBytes, bBytes)
	}
	_ = existing.Close()
	_ = stale.Close()
}
