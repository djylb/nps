package bridge

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/slogx"

	"github.com/djylb/nps/lib/healthcheck"
)

const (
	// healthResync is how often the health groups of a node are compared with
	// the entries even if nothing announced a change.
	healthResync = 30 * time.Second
)

// healthPeer is how the checks reach one connected client. Its calls are made
// from one goroutine and each returns within a bounded time.
type healthPeer interface {
	SetHealthGroup(healthcheck.Group) error // adds targets to a group
	DelHealthGroup(name string) error
}

// healthPush is the state of the health groups of one connection. Its sends
// run as an on-demand task: kick marks it dirty and starts the task unless one
// runs, and the task syncs until nothing is dirty, so at most one sync of a
// connection runs at a time. That task alone changes sent, under mu; mu is never
// held across network I/O.
type healthPush struct {
	store  Store
	peer   healthPeer
	client *Client
	node   *Node
	resync *healthResyncer
	slot   *sync.Map // the slot of resync that holds it

	// mu guards sent and down.
	sent map[string]healthGroup
	down map[string]map[string]bool // group -> targets the checks took out
	id   int
	mu   sync.Mutex

	// run guards dirty, running and closed. wg counts the running task; it is
	// added to only under run while not closed, and waited on only after closed
	// was set, so no Add follows a Wait.
	wg      sync.WaitGroup
	run     sync.Mutex
	end     sync.Once
	dirty   bool
	running bool
	closed  bool
}

// apply takes an unhealthy target out of the rotation of the group's entry, or
// puts a healthy one back. A report of a group this connection no longer has was
// on its way when the group closed; it is dropped, since nothing would ever
// correct it. mu orders it with the clearing of the group's verdicts.
func (p *healthPush) apply(rep healthcheck.Report) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.sent[rep.Group]
	if !ok || !g.target.SetHealthy(rep.Addr, rep.Up) {
		return
	}
	if p.down == nil {
		p.down = make(map[string]map[string]bool)
	}
	was := p.down[rep.Group][rep.Addr]
	switch {
	case !rep.Up && !was:
		if p.down[rep.Group] == nil {
			p.down[rep.Group] = make(map[string]bool)
		}
		p.down[rep.Group][rep.Addr] = true
		slogx.Emit(context.Background(), log, &logevent.TargetDown, "route", g.route, "target", rep.Addr, "reason", "check_failed")
	case rep.Up && was:
		delete(p.down[rep.Group], rep.Addr)
		slogx.Emit(context.Background(), log, &logevent.TargetUp, "route", g.route, "target", rep.Addr, "reason", "check_passed")
	}
}

// remember records a group before it is sent, so that no report of it is missed.
func (p *healthPush) remember(g healthGroup) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent[g.msg.Name] = g
}

// forget closes a group: later reports of it are dropped, and what its checks
// took out goes back in the rotation, since nothing will report on it any more.
func (p *healthPush) forget(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.sent[name]
	if !ok {
		return
	}
	delete(p.sent, name)
	delete(p.down, name)
	for _, addr := range g.msg.Targets {
		g.target.SetHealthy(addr, true)
	}
}

// kick has the task compare the groups with the entries, starting it unless
// it runs; a running task sees the change too. It never blocks.
func (p *healthPush) kick() {
	p.run.Lock()
	defer p.run.Unlock()
	p.dirty = true
	if p.running || p.closed {
		return
	}
	p.running = true
	p.wg.Add(1)
	go p.loop()
}

// loop syncs until nothing is dirty. A failed send ends the push for good, as
// the connection it writes on is broken.
func (p *healthPush) loop() {
	defer p.wg.Done()
	for {
		p.run.Lock()
		if !p.dirty || p.closed {
			p.running = false
			p.run.Unlock()
			return
		}
		p.dirty = false
		p.run.Unlock()
		if err := p.sync(p.id, p.peer, p.client, p.node); err != nil {
			log.Debug("sending health groups failed", "client", p.id, "err", err)
			p.run.Lock()
			p.closed, p.running = true, false
			p.run.Unlock()
			p.finish()
			return
		}
	}
}

// finish ends the push once: the node no longer has it, and the verdicts of
// this connection end with it.
func (p *healthPush) finish() {
	p.end.Do(func() {
		if p.resync != nil {
			p.resync.remove(p)
		}
		p.node.healthPush.CompareAndSwap(p, nil)
		for _, name := range slices.Collect(maps.Keys(p.sent)) {
			p.forget(name)
		}
	})
}

// stop ends the push and waits for a send in flight; each of its calls is
// bounded, so this waits briefly.
func (p *healthPush) stop() {
	p.run.Lock()
	p.closed = true
	p.run.Unlock()
	p.wg.Wait()
	p.finish()
}

func tunnelHealthGroup(id int) string { return "t" + strconv.Itoa(id) }
func hostHealthGroup(id int) string   { return "h" + strconv.Itoa(id) }

// healthGroup is a health group as it should be on a node, or as it was sent.
type healthGroup struct {
	msg    healthcheck.Group
	target *model.Target
	route  string // the route of the entry, for target_down and target_up
}

// same reports whether g needs no new message after o: an edit that replaces
// the Target object starts its health anew even if nothing else changed.
func (g healthGroup) same(o healthGroup) bool {
	return g.target == o.target && g.msg.Type == o.msg.Type && g.msg.Path == o.msg.Path &&
		g.msg.Interval == o.msg.Interval && g.msg.Timeout == o.msg.Timeout && g.msg.Fall == o.msg.Fall &&
		slices.Equal(g.msg.Targets, o.msg.Targets)
}

// newHealthGroup returns the group of target, or false if it needs no checks:
// one address has no other to fail over to.
func newHealthGroup(group, route string, check model.HealthCheck, target *model.Target) (healthGroup, bool) {
	if !check.Enabled {
		return healthGroup{}, false
	}
	addrs := target.Addresses()
	if len(addrs) < 2 {
		return healthGroup{}, false
	}
	check = check.Normalized()
	return healthGroup{target: target, route: route, msg: healthcheck.Group{
		Name: group, Type: check.Type, Path: check.Path, Interval: check.Interval,
		Timeout: check.Timeout, Fall: check.MaxFail, Targets: addrs,
	}}, true
}

// desiredHealthGroups returns the groups node should check. An entry published
// by npc instances has a target per instance, which each checks for itself. Any
// other entry has one target shared by the nodes of the client, and one node
// checks it, so that nodes do not overwrite each other's verdicts.
func desiredHealthGroups(store Store, id int, node *Node, client *Client) map[string]healthGroup {
	groups := make(map[string]healthGroup)
	shared := client.healthNode() == node.UUID
	add := func(group, route string, check model.HealthCheck, routes *model.RouteSet, fallback *model.Target) {
		target := fallback
		if routes.Len() > 0 {
			target = routes.Target(node.UUID, nil)
		} else if !shared {
			return
		}
		if target == nil {
			return
		}
		if g, ok := newHealthGroup(group, route, check, target); ok {
			groups[group] = g
		}
	}
	store.RangeClientTunnels(id, func(v *model.Tunnel) bool {
		v.RLock()
		mine := v.Client != nil && v.Client.Id == id && v.Status && (v.Mode == "tcp" || v.Mode == "secret")
		target := v.Target
		v.RUnlock()
		if mine {
			add(tunnelHealthGroup(v.Id), v.LogRoute(), v.HealthCheck(), v.Routes, target)
		}
		return true
	})
	store.RangeClientHosts(id, func(v *model.Host) bool {
		v.RLock()
		mine := v.Client != nil && v.Client.Id == id && !v.IsClose
		target, domain := v.Target, v.Host
		v.RUnlock()
		if mine {
			add(hostHealthGroup(v.Id), domain, v.HealthCheck(), v.Routes, target)
		}
		return true
	})
	return groups
}

// healthNode returns the UUID of the node that checks the targets its nodes
// share: the first in order that can, or "" if none can.
func (c *Client) healthNode() string {
	var first string
	for _, n := range c.nodeList() {
		if n.healthPush.Load() != nil && !n.IsOffline() && (first == "" || n.UUID < first) {
			first = n.UUID
		}
	}
	return first
}

// startHealthPush starts sending health groups to peer and owning what was
// sent to it. npc forgets the groups with the connection, so a new connection
// starts empty and gets them all. Each write is bounded and fails once the
// connection is closed, so stopping waits briefly at most.
func (s *Bridge) startHealthPush(id int, peer healthPeer, client *Client, node *Node) *healthPush {
	p := &healthPush{store: s.store, id: id, peer: peer, client: client, node: node, sent: make(map[string]healthGroup)}
	node.healthPush.Store(p)
	s.health.add(s, p)
	kickClientHealth(client) // who checks shared targets may have changed
	p.kick()                 // a node not yet in the client's list
	return p
}

// healthSlots is how many slots the periodic resync walks through in one
// healthResync: each tick kicks the pushes of one slot, so the compares are
// spread out instead of all running at once.
const healthSlots = 30

// healthResyncer kicks every push once per healthResync from one ticker,
// which runs while there are pushes.
type healthResyncer struct {
	quit  chan struct{}         // closed when n drops to 0; nil while no ticker runs
	slots [healthSlots]sync.Map // *healthPush -> struct{}
	n     int                   // pushes in the slots, guarded by mu
	mu    sync.Mutex
	next  atomic.Uint32
}

func (r *healthResyncer) add(s *Bridge, p *healthPush) {
	p.resync = r
	p.slot = &r.slots[r.next.Add(1)%healthSlots]
	p.slot.Store(p, struct{}{})
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n++; r.quit == nil {
		r.quit = make(chan struct{})
		go r.loop(r.quit, s.scope.Done())
	}
}

func (r *healthResyncer) remove(p *healthPush) {
	p.slot.Delete(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n--; r.n == 0 {
		close(r.quit)
		r.quit = nil
	}
}

func (r *healthResyncer) loop(quit, done <-chan struct{}) {
	ticker := time.NewTicker(healthResync / healthSlots)
	defer ticker.Stop()
	for i := 0; ; i = (i + 1) % healthSlots {
		select {
		case <-quit:
			return
		case <-done:
			return
		case <-ticker.C:
		}
		r.slots[i].Range(func(k, _ any) bool {
			k.(*healthPush).kick()
			return true
		})
	}
}

// sync makes what node checks through peer match the entries. A changed or
// removed group is deleted first; a changed one is then sent again.
func (p *healthPush) sync(id int, peer healthPeer, client *Client, node *Node) error {
	want := desiredHealthGroups(p.store, id, node, client)
	for name, old := range p.sent {
		if g, ok := want[name]; ok && g.same(old) {
			continue
		}
		p.forget(name)
		if err := peer.DelHealthGroup(name); err != nil {
			return err
		}
	}
	for name, g := range want {
		if _, ok := p.sent[name]; ok {
			continue
		}
		p.remember(g)
		if err := peer.SetHealthGroup(g.msg); err != nil {
			return err
		}
	}
	return nil
}

// kickHealth has every node compare its health groups with the entries. It
// never blocks: a sender busy with an earlier change sees this one too.
func (s *Bridge) kickHealth() {
	s.Client.Range(func(_, value any) bool {
		if c, ok := value.(*Client); ok && c != nil {
			kickClientHealth(c)
		}
		return true
	})
}

func kickClientHealth(c *Client) {
	for _, n := range c.nodeList() {
		if p := n.healthPush.Load(); p != nil {
			p.kick()
		}
	}
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.pingClients()
		case <-s.scope.Done():
			return
		}
	}
}

// pingClients closes the clients that had no online node for three rounds.
func (s *Bridge) pingClients() {
	closedClients := make([]int, 0)
	s.Client.Range(func(key, value any) bool {
		clientID := key.(int)
		if clientID <= 0 {
			return true
		}
		client, ok := value.(*Client)
		if !ok || client == nil {
			log.Debug("empty client entry removed", "client", clientID)
			closedClients = append(closedClients, clientID)
			return true
		}
		client.RemoveOfflineNodes(false)
		node := client.CheckNode()
		if (node == nil || node.IsOffline()) && !client.awaitingTunnel() {
			client.retryTime++
			if client.retryTime >= 3 {
				closedClients = append(closedClients, clientID)
			}
		} else {
			client.retryTime = 0 // Reset retry count when the state is normal
		}
		return true
	})

	for _, clientId := range closedClients {
		// its connections closed and it did not come back within three checks
		s.delClient(clientId, "closed")
	}
}
