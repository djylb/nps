package bridge

import (
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
)

func TestNewConfigIgnoresClientIdentity(t *testing.T) {
	_ = file.GetDb()
	useTestDb(t)
	existing := file.NewClient("existing-vkey", false, false)
	existing.Id = 7
	if err := file.GetDb().NewClient(existing); err != nil {
		t.Fatal(err)
	}

	b := NewTunnel(&sync.Map{}, 30)
	clientSide, serverSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(conn.NewConn(serverSide), true, nil, legacy.LatestIndex, "", "node-1")
	}()

	c := conn.NewConn(clientSide)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	sent := file.NewClient(existing.VerifyKey, true, true)
	sent.Id = existing.Id
	if err := legacy.WriteClient(c, sent); err != nil {
		t.Fatal(err)
	}
	if ok, _ := legacy.ReadStatus(c); !ok {
		t.Fatal("config was not accepted")
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.Close()
	<-done

	if string(buf) == existing.VerifyKey {
		t.Fatal("server returned the submitted verify key")
	}
	stored, err := file.GetDb().GetClient(existing.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored != existing || stored.VerifyKey != "existing-vkey" || stored.NoStore {
		t.Fatalf("stored client %d changed: %+v", existing.Id, stored)
	}
	id, err := file.GetDb().GetClientIdByBlake2bVkey(crypt.Blake2b(string(buf)))
	if err != nil || id == existing.Id {
		t.Fatalf("new client id = %d, %v", id, err)
	}
}

// A port conflict fails that task only: npc reads one failure for it, goes on
// with its next task and keeps its connection, and the statuses stay aligned.
func TestConfigPortConflictFailsOnlyThatTask(t *testing.T) {
	_ = file.GetDb()
	useTestDb(t)
	client := file.NewClient("conflict-vkey", false, false)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatal(err)
	}
	taken, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	takenPort := taken.Addr().(*net.TCPAddr).Port
	free, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()

	b := NewTunnel(&sync.Map{}, 30)
	b.OpenTask = make(chan *file.Tunnel, 8)
	b.Client.Store(client.Id, NewClient(client.Id, NewNode("node", "", legacy.LatestIndex)))
	clientSide, serverSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(conn.NewConn(serverSide), false, client, legacy.LatestIndex, "", "node")
	}()
	c := conn.NewConn(clientSide)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	send := func(port int, remark string) bool {
		task := &file.Tunnel{Mode: "tcp", Ports: strconv.Itoa(port), Remark: remark, Target: &file.Target{TargetStr: "127.0.0.1:80"}}
		if err := legacy.WriteTask(c, task); err != nil {
			t.Fatal(err)
		}
		ok, _ := legacy.ReadStatus(c)
		return ok
	}
	if send(takenPort, "conflict") {
		t.Fatal("task on a taken port was accepted")
	}
	if !send(freePort, "ok") {
		t.Fatal("next task failed after a conflict")
	}
	_ = clientSide.Close()
	<-done

	if _, ok := b.Client.Load(client.Id); !ok {
		t.Fatal("port conflict removed the client")
	}
	var remarks []string
	file.GetDb().JsonDb.Tasks.Range(func(_, v any) bool {
		remarks = append(remarks, v.(*file.Tunnel).Remark)
		return true
	})
	if len(remarks) != 1 || remarks[0] != "ok" {
		t.Fatalf("stored tasks = %v", remarks)
	}
}

func TestJoinNodeReplacesClosedClient(t *testing.T) {
	_ = file.GetDb()
	useTestDb(t)
	b := NewTunnel(&sync.Map{}, 30)
	closed := NewClient(9, NewNode("old", "", 8))
	_ = closed.Close()
	b.Client.Store(9, closed)

	client, node := b.joinNode(9, "new", NewNode("new", "", 8), func(*Node) {})
	if client == closed {
		t.Fatal("node joined a closed client")
	}
	v, _ := b.Client.Load(9)
	if v != client {
		t.Fatal("the joined client is not registered")
	}
	if n, ok := client.GetNodeByUUID("new"); !ok || n != node {
		t.Fatal("the node is not reachable through its client")
	}
}
