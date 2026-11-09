package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
)

// updateServer is a server holding client 1, connected through one npc of
// version vs, with the given latest release and remote update setting.
func updateServer(t *testing.T, vs, latest string, disabled bool) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conf", "clients.json"), []byte(`[{"Id":1,"VerifyKey":"a"},{"Id":2,"VerifyKey":"b"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	db := file.New(dir)
	db.JsonDb.LoadClientFromJsonFile()
	s := New(db, Config{CoreEntry: stubEntry{}, DisconnectTimeout: 30, DisableRemoteUpdate: disabled,
		LatestVersion: func() string { return latest }})
	t.Cleanup(func() { _ = s.Close() })
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	n := bridge.NewNode("n", vs, legacy.LatestIndex)
	n.AddSignal(conn.NewConn(a))
	s.bridge.Client.Store(1, bridge.NewClient(1, n, bridge.Primary))
	return s
}

// The client list says whether a connected npc can be updated.
func TestListClientsCanUpdate(t *testing.T) {
	for _, c := range []struct {
		name     string
		vs       string
		latest   string
		disabled bool
		want     bool
	}{
		{"older than latest", "0.34.9", "0.35.0", false, true},
		{"equal to latest", "0.35.0", "0.35.0", false, false},
		{"newer than latest", "0.35.1", "0.35.0", false, false},
		{"npc without the capability", "0.34.8", "0.35.0", false, false},
		{"latest unknown, older than the server", "0.34.9", "", false, true},
		{"server self-update disabled", "0.34.9", "0.35.0", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := updateServer(t, c.vs, c.latest, c.disabled)
			list, _ := s.ListClients(0, 10, "", "Id", "asc", 0)
			got := map[int]bool{}
			for _, cl := range list {
				got[cl.Id] = cl.CanUpdate
			}
			if got[1] != c.want {
				t.Fatalf("connected client: CanUpdate = %v, want %v", got[1], c.want)
			}
			if got[2] {
				t.Fatal("offline client: CanUpdate")
			}
		})
	}
}

// Disabling the web update of the server leaves client updates alone.
func TestRequestClientUpdateIgnoresSelfUpdateSwitch(t *testing.T) {
	s := updateServer(t, "0.34.9", "0.35.0", true)
	if _, err := s.RequestClientUpdate(1); errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateSelfDisabled(t *testing.T) {
	s := New(file.New(t.TempDir()), Config{CoreEntry: stubEntry{}, DisableRemoteUpdate: true})
	t.Cleanup(func() { _ = s.Close() })
	if s.CanUpdateSelf() {
		t.Fatal("CanUpdateSelf with remote update disabled")
	}
	if v, err := s.UpdateSelf(); v != "" || !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("UpdateSelf = %q, %v; want ErrUpdateDisabled", v, err)
	}
	if s.updating.Load() {
		t.Fatal("a refused update left the update marked as running")
	}
}

// Only a connected client with shaping on lists shaping statistics.
func TestClientShapedListing(t *testing.T) {
	s := updateServer(t, "0.34.9", "0.35.0", false)
	for _, shaping := range []bool{false, true} {
		v, _ := s.db.JsonDb.Clients.Load(1)
		v.(*model.Client).Cnf = &model.Config{Shaping: shaping}
		list, _ := s.ListClients(0, 10, "", "Id", "asc", 0)
		for _, c := range list {
			// the node of updateServer has a signal connection only: no core session
			if c.Shaped != nil {
				t.Fatalf("shaping %v: client %d lists stats without a core session", shaping, c.Id)
			}
		}
	}
	if s.clientShaped(&model.Client{Id: 1, Cnf: &model.Config{}}) != nil {
		t.Fatal("stats of a client without shaping")
	}
}
