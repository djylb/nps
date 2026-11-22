package bridge

import (
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/model"
)

// recordPeer keeps the groups it was sent and how many calls ran at once.
type recordPeer struct {
	groups  map[string]healthcheck.Group
	hold    chan struct{} // if set, each call waits for it to close
	entered chan struct{} // if set, gets a value when a call starts
	mu      sync.Mutex
	active  atomic.Int32
	maxSeen atomic.Int32
	calls   atomic.Int32
}

func (p *recordPeer) enter() {
	p.calls.Add(1)
	n := p.active.Add(1)
	for m := p.maxSeen.Load(); n > m && !p.maxSeen.CompareAndSwap(m, n); m = p.maxSeen.Load() {
	}
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if p.hold != nil {
		<-p.hold
	}
	time.Sleep(100 * time.Microsecond)
}

func (p *recordPeer) SetHealthGroup(g healthcheck.Group) error {
	p.enter()
	defer p.active.Add(-1)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.groups[g.Name] = g
	return nil
}

func (p *recordPeer) DelHealthGroup(name string) error {
	p.enter()
	defer p.active.Add(-1)
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.groups, name)
	return nil
}

// onlineNode gives node an open signal connection and tunnel.
func onlineNode(t *testing.T, node *Node) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	node.AddSignal(conn.NewConn(b))
	node.AddTunnel(&fakeOpenTunnel{})
}

func (p *healthPush) idle() bool {
	p.run.Lock()
	defer p.run.Unlock()
	return !p.running && !p.dirty
}

// A storm of kicks while entries change runs one sync of a connection at a
// time, and what the peer has in the end matches the latest entries.
func TestHealthPushKickStorm(t *testing.T) {
	db, client, tn := healthEnv(t)
	s := NewTunnel(db, nil, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	node := NewNode("n1", "0.35.0", legacy.LatestIndex)
	onlineNode(t, node)
	c := NewClient(client.Id, node, Primary)
	s.Client.Store(client.Id, c)
	peer := &recordPeer{groups: map[string]healthcheck.Group{}}
	p := s.startHealthPush(client.Id, peer, c, node)
	defer p.stop()

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 50 {
				if w == 0 {
					tg := &model.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:" + strconv.Itoa(2+i%5)}
					tn.Lock()
					tn.Target = tg
					tn.Unlock()
					db.TunnelChanged(tn)
				}
				s.EntriesChanged()
			}
		})
	}
	wg.Wait()
	for end := time.Now().Add(10 * time.Second); !p.idle(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("push did not settle")
		}
	}
	if m := peer.maxSeen.Load(); m != 1 {
		t.Fatalf("%d sends ran at once", m)
	}
	want := desiredHealthGroups(db, client.Id, node, c)
	if len(want) == 0 {
		t.Fatal("no groups wanted")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.groups) != len(want) {
		t.Fatalf("peer has %d groups, want %d", len(peer.groups), len(want))
	}
	for name, g := range want {
		if got, ok := peer.groups[name]; !ok || !slices.Equal(got.Targets, g.msg.Targets) {
			t.Fatalf("group %s: %+v, want %+v", name, got, g.msg)
		}
	}
	if peer.calls.Load() >= 400 {
		t.Fatalf("%d sends for 400 kicks: kicks were not merged", peer.calls.Load())
	}
}

// stop waits for the send in flight, and nothing is sent after it.
func TestHealthPushStopWaitsInFlight(t *testing.T) {
	db, client, _ := healthEnv(t)
	s := NewTunnel(db, nil, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	node := NewNode("n1", "0.35.0", legacy.LatestIndex)
	onlineNode(t, node)
	c := NewClient(client.Id, node, Primary)
	peer := &recordPeer{groups: map[string]healthcheck.Group{}, hold: make(chan struct{}), entered: make(chan struct{}, 1)}
	p := s.startHealthPush(client.Id, peer, c, node)
	select {
	case <-peer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no send started")
	}
	stopped := make(chan struct{})
	go func() { p.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned with a send in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(peer.hold)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the send ended")
	}
	if node.healthPush.Load() != nil {
		t.Fatal("node still has the push")
	}
	n := peer.calls.Load()
	p.kick()
	kickClientHealth(c)
	time.Sleep(20 * time.Millisecond)
	if peer.calls.Load() != n {
		t.Fatal("sent after stop")
	}
	if s.health.quit != nil {
		t.Fatal("resync ticker still runs without pushes")
	}
}

// One kick makes every session compute its groups: with 1000 sessions and 5000
// tunnels, "index" is the cost now and "scan" that of a scan of all tunnels per
// session, as before the index.
func BenchmarkHealthKick(b *testing.B) {
	const clients, tunnels = 1000, 5000
	db, _, _ := healthEnv(b)
	cs := make([]*model.Client, clients)
	for i := range cs {
		cs[i] = &model.Client{Id: 100 + i, Cnf: &model.Config{}, Flow: &model.Flow{}}
		db.JsonDb.Clients.Store(cs[i].Id, cs[i])
	}
	for i := range tunnels {
		tn := &model.Tunnel{Id: 100 + i, Mode: "tcp", Status: true, Client: cs[i%clients], Target: &model.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:2"}}
		tn.SetHealthCheck(model.HealthCheck{Enabled: true})
		db.JsonDb.Tasks.Store(tn.Id, tn)
		db.TunnelChanged(tn)
	}
	b.Run("index", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cs {
				n := 0
				db.RangeClientTunnels(c.Id, func(*model.Tunnel) bool { n++; return true })
				db.RangeClientHosts(c.Id, func(*model.Host) bool { n++; return true })
			}
		}
	})
	b.Run("scan", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cs {
				n := 0
				db.RangeTunnels(func(v *model.Tunnel) bool {
					v.RLock()
					if v.Client != nil && v.Client.Id == c.Id {
						n++
					}
					v.RUnlock()
					return true
				})
				db.RangeHosts(func(*model.Host) bool { return true })
			}
		}
	})
}
