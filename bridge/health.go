package bridge

import (
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/logs"
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

// healthPush is the state of the health groups of one connection. Its goroutine
// alone changes sent, under mu; mu is never held across network I/O.
type healthPush struct {
	store Store
	mu    sync.Mutex
	sent  map[string]healthGroup
	quit  chan struct{}
	done  chan struct{}
}

// apply takes an unhealthy target out of the rotation of the group's entry, or
// puts a healthy one back. A report of a group this connection no longer has was
// on its way when the group closed; it is dropped, since nothing would ever
// correct it. mu orders it with the clearing of the group's verdicts.
func (p *healthPush) apply(rep healthcheck.Report) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.sent[rep.Group]; ok {
		g.target.SetHealthy(rep.Addr, rep.Up)
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
	for _, addr := range g.msg.Targets {
		g.target.SetHealthy(addr, true)
	}
}

// stop ends the goroutine. Each of its calls is bounded, so this waits briefly.
func (p *healthPush) stop() {
	close(p.quit)
	<-p.done
}

func tunnelHealthGroup(id int) string { return "t" + strconv.Itoa(id) }
func hostHealthGroup(id int) string   { return "h" + strconv.Itoa(id) }

// healthGroup is a health group as it should be on a node, or as it was sent.
type healthGroup struct {
	msg    healthcheck.Group
	target *model.Target
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
func newHealthGroup(group string, check model.HealthCheck, target *model.Target) (healthGroup, bool) {
	if !check.Enabled {
		return healthGroup{}, false
	}
	addrs := target.Addresses()
	if len(addrs) < 2 {
		return healthGroup{}, false
	}
	check = check.Normalized()
	return healthGroup{target: target, msg: healthcheck.Group{
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
	add := func(group string, check model.HealthCheck, routes *model.RouteSet, fallback *model.Target) {
		target := fallback
		if routes.Len() > 0 {
			target = routes.Target(node.UUID, nil)
		} else if !shared {
			return
		}
		if target == nil {
			return
		}
		if g, ok := newHealthGroup(group, check, target); ok {
			groups[group] = g
		}
	}
	store.RangeTunnels(func(v *model.Tunnel) bool {
		v.RLock()
		mine := v.Client != nil && v.Client.Id == id && v.Status && (v.Mode == "tcp" || v.Mode == "secret")
		target := v.Target
		v.RUnlock()
		if mine {
			add(tunnelHealthGroup(v.Id), v.HealthCheck(), v.Routes, target)
		}
		return true
	})
	store.RangeHosts(func(v *model.Host) bool {
		v.RLock()
		mine := v.Client != nil && v.Client.Id == id && !v.IsClose
		target := v.Target
		v.RUnlock()
		if mine {
			add(hostHealthGroup(v.Id), v.HealthCheck(), v.Routes, target)
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
		if n.healthKick.Load() != nil && !n.IsOffline() && (first == "" || n.UUID < first) {
			first = n.UUID
		}
	}
	return first
}

// startHealthPush starts the one goroutine that sends health groups to peer and
// owns what was sent to it. npc forgets the groups with the
// connection, so a new connection starts empty and gets them all. It returns
// after the goroutine ended; since each write is bounded and fails once c is
// closed, stopping waits briefly at most.
func (s *Bridge) startHealthPush(id int, peer healthPeer, client *Client, node *Node) *healthPush {
	kick := make(chan struct{}, 1)
	p := &healthPush{store: s.store, sent: make(map[string]healthGroup), quit: make(chan struct{}), done: make(chan struct{})}
	node.healthKick.Store(&kick)
	kickClientHealth(client) // who checks shared targets may have changed
	go func() {
		defer close(p.done)
		defer node.healthKick.CompareAndSwap(&kick, nil)
		// The verdicts of this connection end with it.
		defer func() {
			for _, name := range slices.Collect(maps.Keys(p.sent)) {
				p.forget(name)
			}
		}()
		ticker := time.NewTicker(healthResync)
		defer ticker.Stop()
		for {
			if err := p.sync(id, peer, client, node); err != nil {
				logs.Trace("send health groups to client %d: %v", id, err)
				return
			}
			select {
			case <-p.quit:
				return
			case <-kick:
			case <-ticker.C:
			}
		}
	}()
	return p
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
		if kick := n.healthKick.Load(); kick != nil {
			select {
			case *kick <- struct{}{}:
			default:
			}
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
	s.Client.Range(func(key, value interface{}) bool {
		clientID := key.(int)
		if clientID <= 0 {
			return true
		}
		client, ok := value.(*Client)
		if !ok || client == nil {
			logs.Trace("Client %d is nil", clientID)
			closedClients = append(closedClients, clientID)
			return true
		}
		client.RemoveOfflineNodes(false)
		node := client.CheckNode()
		if (node == nil || node.IsOffline()) && !client.awaitingTunnel() {
			client.retryTime++
			if client.retryTime >= 3 {
				logs.Trace("Stop client %d", clientID)
				closedClients = append(closedClients, clientID)
			}
		} else {
			client.retryTime = 0 // Reset retry count when the state is normal
		}
		return true
	})

	for _, clientId := range closedClients {
		logs.Info("the client %d closed", clientId)
		s.DelClient(clientId)
	}
}
