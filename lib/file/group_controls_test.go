package file

import (
	"github.com/djylb/nps/lib/rate"
	"testing"
)

func TestAcceptedTunnelMoveKeepsLibrarySlots(t *testing.T) {
	db := newTestDb(t)
	a, b := &Client{Id: 1, MaxConn: 1}, &Client{Id: 2, MaxConn: 1}
	task := &Tunnel{Id: 1, Client: a, MaxConn: 1}
	if err := db.NewTask(task); err != nil {
		t.Fatal(err)
	}
	group := task.ControlGroup()
	if err := group.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer group.Release()
	edit := task.Clone()
	edit.Client = b
	if err := db.UpdateTask(edit); err != nil {
		t.Fatal(err)
	}
	if edit.ControlGroup() != group || a.CurrentConnections() != 0 || b.CurrentConnections() != 1 {
		t.Fatal("accepted edit lost/repeated live slots")
	}
	if edit.GetConn() {
		t.Fatal("edited leaf ignored existing reservation")
	}
}

func TestClientUpdateRetainsRuntimeGroup(t *testing.T) {
	db := newTestDb(t)
	original := &Client{Id: 1, Rate: rate.NewRate(0), Flow: &Flow{}}
	db.JsonDb.Clients.Store(1, original)
	g := original.ControlGroup()
	if err := g.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer g.Release()
	edit := &Client{Id: 1, Rate: rate.NewRate(10), RateLimit: 12, MaxConn: 1, Flow: original.Flow}
	if err := db.UpdateClient(edit); err != nil {
		t.Fatal(err)
	}
	if edit.ControlGroup() != g || edit.CurrentConnections() != 1 {
		t.Fatal("replacing client detached live group")
	}
	if edit.GetConn() {
		t.Fatal("updated client ignored the active slot")
	}
}
