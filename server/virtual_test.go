package server

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
)

func freeTunnelPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// listening reports whether something accepts on port.
func listening(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

type backendFixture struct {
	s       *Server
	db      *file.DbUtils
	client  *model.Client
	backend *model.Tunnel
	port    int
	tcp     *model.Tunnel
}

func newBackendFixture(t *testing.T, noStore bool) *backendFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(dir)
	c := model.NewClient("vk", false, false)
	c.Status = true
	if err := db.NewClient(c); err != nil {
		t.Fatal(err)
	}
	s := New(db, Config{DisconnectTimeout: 30})
	t.Cleanup(func() { _ = s.Close() })
	f := &backendFixture{s: s, db: db, client: c, port: freeTunnelPort(t)}
	target := model.ServiceTarget(model.FileServiceKind, "port-"+strconv.Itoa(f.port))
	f.backend = &model.Tunnel{Id: db.NextTunnelID(), Mode: "file", Client: c, Status: true, NoStore: noStore, Flow: new(model.Flow), Target: &model.Target{TargetStr: target}}
	if err := db.NewTask(f.backend); err != nil {
		t.Fatal(err)
	}
	f.tcp = &model.Tunnel{Id: db.NextTunnelID(), Mode: "tcp", Port: f.port, ServerIp: "127.0.0.1", Client: c, Status: true, NoStore: noStore, Flow: new(model.Flow), Target: &model.Target{TargetStr: target}}
	if !noStore {
		f.tcp.BackendOf = f.backend.Id
	}
	if err := db.NewTask(f.tcp); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(f.backend); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(f.tcp); err != nil {
		t.Fatal(err)
	}
	if !listening(f.port) {
		t.Fatal("generated tunnel does not listen")
	}
	return f
}

func (f *backendFixture) assertGone(t *testing.T) {
	t.Helper()
	if _, err := f.db.GetTask(f.tcp.Id); err == nil {
		t.Fatal("generated tunnel still stored")
	}
	for end := time.Now().Add(3 * time.Second); listening(f.port); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("port still listening")
		}
	}
}

func TestDeleteBackendDeletesGeneratedTunnel(t *testing.T) {
	f := newBackendFixture(t, false)
	if err := f.s.DeleteTunnel(f.backend.Id); err != nil {
		t.Fatal(err)
	}
	f.assertGone(t)
}

func TestDeleteClientDeletesGeneratedTunnel(t *testing.T) {
	f := newBackendFixture(t, false)
	f.s.DeleteClientResources(f.client.Id)
	f.assertGone(t)
}

func TestUploadedTunnelGoesWithItsClient(t *testing.T) {
	f := newBackendFixture(t, true)
	f.s.deleteClientEntries(f.client.Id, true)
	f.assertGone(t)
	if _, err := f.db.GetTask(f.backend.Id); err == nil {
		t.Fatal("uploaded backend still stored")
	}
}
