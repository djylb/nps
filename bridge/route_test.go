package bridge

import (
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"context"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// routeNode is one connected npc instance: its mux stream carries the links
// the bridge opens to it.
type routeNode struct {
	node  *Node
	links chan string
	close func()
}

func joinRouteNode(t *testing.T, b *Bridge, clientId int, uuid string) *routeNode {
	t.Helper()
	bridgeSide, npcSide := net.Pipe()
	signal, signalPeer := net.Pipe()
	serverMux := legacy.NewMuxCarrier(bridgeSide, "tcp", 30, true)
	npcMux := legacy.NewMuxCarrier(npcSide, "tcp", 30, false)
	n := &routeNode{links: make(chan string, 16)}
	_, n.node = b.joinNode(clientId, uuid, NewNode(uuid, "", legacy.LatestIndex), func(*Node) {})
	n.node.AddSignal(conn.NewConn(signal))
	n.node.AddTunnel(serverMux)
	go func() {
		for {
			c, err := npcMux.AcceptStream(context.Background())
			if err != nil {
				return
			}
			lk, err := legacy.ReadLink(c)
			if err == nil {
				n.links <- lk.Host
			}
			_ = c.Close()
		}
	}()
	n.close = func() {
		_ = npcMux.Close()
		_ = signalPeer.Close()
		n.node.CloseIfSignalCurrent(n.node.GetSignal())
		v, _ := b.Client.Load(clientId)
		v.(*Client).closeAndRemoveNodeIfCurrent(uuid, n.node, (*Node).IsOffline)
	}
	t.Cleanup(func() {
		_ = npcMux.Close()
		_ = serverMux.Close()
		_ = signalPeer.Close()
	})
	return n
}

// publish sends the config messages of uuid through a config connection.
func publish(t *testing.T, b *Bridge, client *file.Client, uuid string, ver int, flag string, msgs ...any) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(conn.NewConn(serverSide), false, client, ver, "", uuid)
	}()
	c := conn.NewConn(clientSide)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	for _, m := range msgs {
		if err := legacy.WriteSignal(c, m, flag); err != nil {
			t.Fatal(err)
		}
		if ok, err := legacy.ReadStatus(c); !ok || err != nil {
			t.Fatalf("publish refused: %v", err)
		}
	}
	_ = clientSide.Close()
	<-done
}

func routeFixture(t *testing.T) (*Bridge, *file.Client) {
	t.Helper()
	_ = file.GetDb()
	useTestDb(t)
	client := file.NewClient("route-vkey", false, false)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatal(err)
	}
	b := NewTunnel(&sync.Map{}, 30)
	return b, client
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// openRoute opens one link of route the way the proxies do.
func openRoute(b *Bridge, clientId int, r file.Route) error {
	return r.Target.Dial(func(addr string) (bool, error) {
		link := conn.NewLink(common.ConnTCP, addr, false, false, "", false)
		link.Option.Node = r.Node
		c, err := b.Session(clientId).OpenLink(link, 0)
		if err == nil {
			_ = c.Close()
		}
		return false, err
	})
}

func TestTaskRoutesAcrossInstances(t *testing.T) {
	b, client := routeFixture(t)
	a := joinRouteNode(t, b, client.Id, "a")
	c := joinRouteNode(t, b, client.Id, "c")
	port := strconv.Itoa(freePort(t))
	task := func(target string) *file.Tunnel {
		return &file.Tunnel{Mode: "tcp", Ports: port, Remark: "r", Target: &file.Target{TargetStr: target}}
	}
	publish(t, b, client, "a", legacy.LatestIndex, legacy.FlagNewTask, task("127.0.0.1:1"))
	opened := <-b.OpenTask
	publish(t, b, client, "c", legacy.LatestIndex, legacy.FlagNewTask, task("127.0.0.1:2"))
	select {
	case <-b.OpenTask:
		t.Fatal("a joining instance restarted the running task")
	default:
	}
	stored, ok := client.HasTunnel(opened)
	if !ok || stored != opened {
		t.Fatal("the second instance created another entry")
	}

	// Requests alternate, and each instance gets its own target.
	for range 2 {
		for range 2 {
			if err := openRoute(b, client.Id, stored.Route()); err != nil {
				t.Fatal(err)
			}
		}
		if got := <-a.links; got != "127.0.0.1:1" {
			t.Fatalf("node a got target %q", got)
		}
		if got := <-c.links; got != "127.0.0.1:2" {
			t.Fatalf("node c got target %q", got)
		}
	}

	var deleted []int
	del := func(id int) { deleted = append(deleted, id) }
	a.close()
	b.PruneRoutes(del, del)
	if len(deleted) != 0 || stored.Routes.Len() != 1 {
		t.Fatalf("deleted %v, %d routes left, want the entry with one route", deleted, stored.Routes.Len())
	}
	if stored.Target.TargetStr != "127.0.0.1:2" {
		t.Fatalf("entry target = %q", stored.Target.TargetStr)
	}
	for range 3 {
		if err := openRoute(b, client.Id, stored.Route()); err != nil {
			t.Fatal(err)
		}
		if got := <-c.links; got != "127.0.0.1:2" {
			t.Fatalf("remaining node got target %q", got)
		}
	}

	c.close()
	b.PruneRoutes(del, del)
	if len(deleted) != 1 || deleted[0] != stored.Id {
		t.Fatalf("deleted %v, want task %d", deleted, stored.Id)
	}
}

func TestHostRoutesAndStoredEntries(t *testing.T) {
	b, client := routeFixture(t)
	a := joinRouteNode(t, b, client.Id, "a")
	c := joinRouteNode(t, b, client.Id, "c")
	stored := &file.Host{Host: "web.example.com", Location: "/", Client: client, Flow: new(file.Flow), Target: &file.Target{TargetStr: "127.0.0.1:9"}}
	if err := file.GetDb().NewHost(stored); err != nil {
		t.Fatal(err)
	}
	host := func(target string) *file.Host {
		return &file.Host{Host: "npc.example.com", Remark: "r", Target: &file.Target{TargetStr: target}}
	}
	publish(t, b, client, "a", legacy.LatestIndex, legacy.FlagNewHost, host("127.0.0.1:1"))
	publish(t, b, client, "c", legacy.LatestIndex, legacy.FlagNewHost, host("127.0.0.1:2"))
	entry, ok := client.HasHost(&file.Host{Host: "npc.example.com", Location: "/"})
	if !ok || entry.Routes.Len() != 2 {
		t.Fatalf("entry %+v, want one host with two routes", entry)
	}
	seen := map[string]bool{}
	for range 2 {
		seen[entry.Route().Node] = true
	}
	if !seen["a"] || !seen["c"] {
		t.Fatalf("picked %v, want both nodes", seen)
	}

	// A web-managed entry has no routes and uses the select mode.
	if r := stored.Route(); r.Node != "" || r.Target != stored.Target {
		t.Fatalf("stored host route = %+v", r)
	}

	a.close()
	c.close()
	var deleted []int
	b.PruneRoutes(func(int) { t.Error("no task to delete") }, func(id int) { deleted = append(deleted, id) })
	if len(deleted) != 1 || deleted[0] != entry.Id {
		t.Fatalf("deleted %v, want host %d only", deleted, entry.Id)
	}
}

// A client without a stable UUID keeps the previous behaviour: no routes,
// and the entry stays until the client goes.
func TestLegacyClientHasNoRoutes(t *testing.T) {
	b, client := routeFixture(t)
	const ver = 5
	b.Client.Store(client.Id, NewClient(client.Id, NewNode("main-conn", "", ver)))
	publish(t, b, client, "config-conn", ver, legacy.FlagNewTask, &file.Tunnel{
		Mode: "tcp", Ports: strconv.Itoa(freePort(t)), Remark: "r", Target: &file.Target{TargetStr: "127.0.0.1:1"},
	})
	task := <-b.OpenTask
	if r := task.Route(); r.Node != "" || r.Target != task.Target {
		t.Fatalf("legacy route = %+v", r)
	}
	v, _ := b.Client.Load(client.Id)
	if _, ok := v.(*Client).GetNodeByUUID("config-conn"); ok {
		t.Fatal("the config connection of a legacy client became a node")
	}
	b.PruneRoutes(func(int) { t.Error("legacy task deleted") }, func(int) { t.Error("legacy host deleted") })
}

func TestLinkNodeStaysOffTheWire(t *testing.T) {
	link := conn.NewLink(common.ConnTCP, "h", false, false, "", false)
	plain, _ := json.Marshal(link)
	link.Option.Node = "a"
	routed, _ := json.Marshal(link)
	if string(plain) != string(routed) {
		t.Fatalf("link changed on the wire: %s", routed)
	}
}

func TestDeadRouteNodeDoesNotCloseTheClient(t *testing.T) {
	b, client := routeFixture(t)
	joinRouteNode(t, b, client.Id, "a")
	link := conn.NewLink(common.ConnTCP, "h", false, false, "", false)
	link.Option.Node = "gone"
	if _, err := b.Session(client.Id).OpenLink(link, 0); err == nil {
		t.Fatal("a link through a missing node succeeded")
	}
	if _, ok := b.Client.Load(client.Id); !ok {
		t.Fatal("the client was closed")
	}
}

// A close signal handled after the client came back leaves its entries alone.
func TestCloseSignalSparesAReturningClient(t *testing.T) {
	b, client := routeFixture(t)
	const ver = legacy.LatestIndex
	cleanups := 0
	cleanup := func() { cleanups++ }

	// Gone, and still gone when the signal is handled.
	b.Client.Store(client.Id, NewClient(client.Id, NewNode("n1", "", ver)))
	b.DelClient(client.Id)
	<-b.CloseClient
	if !b.CloseIfDisconnected(client.Id, cleanup) || cleanups != 1 {
		t.Fatalf("a client that stayed gone was not cleaned up: %d", cleanups)
	}

	// Reconnected before the signal is handled.
	b.Client.Store(client.Id, NewClient(client.Id, NewNode("n2", "", ver)))
	b.DelClient(client.Id)
	<-b.CloseClient
	b.Client.Store(client.Id, NewClient(client.Id, NewNode("n3", "", ver)))
	if b.CloseIfDisconnected(client.Id, cleanup) || cleanups != 1 {
		t.Fatal("a reconnected client was cleaned up")
	}
	b.DelClient(client.Id)
	<-b.CloseClient

	// Published without a session yet, as clients before NodeID do.
	publish(t, b, client, "config-conn", 5, legacy.FlagNewTask, &file.Tunnel{
		Mode: "tcp", Ports: strconv.Itoa(freePort(t)), Remark: "again", Target: &file.Target{TargetStr: "127.0.0.1:1"},
	})
	<-b.OpenTask
	b.Client.Delete(client.Id)
	if b.CloseIfDisconnected(client.Id, cleanup) || cleanups != 1 {
		t.Fatal("entries published after the client left were cleaned up")
	}
}
