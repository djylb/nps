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

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func generatedFixture(t *testing.T, noStore bool) (*Server, *file.DbUtils, *model.Client, *model.Tunnel) {
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
	s := New(db, Config{CoreEntry: stubEntry{}, DisconnectTimeout: 30})
	t.Cleanup(func() { _ = s.Close() })
	tn := &model.Tunnel{Id: db.NextTunnelID(), Mode: "tcp", Port: freeTunnelPort(t), ServerIp: "127.0.0.1", Client: c, Status: true,
		NoStore: noStore, Generated: !noStore, Flow: new(model.Flow), Target: &model.Target{TargetStr: model.ServiceTarget("file", "x")}}
	if err := db.NewTask(tn); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(tn); err != nil {
		t.Fatal(err)
	}
	if !listening(tn.Port) {
		t.Fatal("does not listen")
	}
	return s, db, c, tn
}

func assertGone(t *testing.T, db *file.DbUtils, tn *model.Tunnel) {
	t.Helper()
	if _, err := db.GetTask(tn.Id); err == nil {
		t.Fatal("still stored")
	}
	for end := time.Now().Add(3 * time.Second); listening(tn.Port); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("still listening")
		}
	}
}

func TestGeneratedTunnelGoesWithItsClient(t *testing.T) {
	s, db, c, tn := generatedFixture(t, false)
	s.DeleteClientResources(c.Id)
	assertGone(t, db, tn)
}

func TestGeneratedTunnelDeletedByUser(t *testing.T) {
	s, db, _, tn := generatedFixture(t, false)
	if err := s.DeleteTunnel(tn.Id); err != nil {
		t.Fatal(err)
	}
	assertGone(t, db, tn)
}

// A stored generated tunnel stays when the client goes offline; one from an
// upload goes.
func TestOfflineKeepsStoredGeneratedTunnel(t *testing.T) {
	s, db, c, tn := generatedFixture(t, false)
	s.deleteClientEntries(c.Id, true)
	if _, err := db.GetTask(tn.Id); err != nil {
		t.Fatal("stored tunnel removed:", err)
	}
}

func TestUploadedTunnelGoesWhenOffline(t *testing.T) {
	s, db, c, tn := generatedFixture(t, true)
	s.deleteClientEntries(c.Id, true)
	assertGone(t, db, tn)
}
