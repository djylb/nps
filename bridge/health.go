package bridge

import (
	"slices"
	"strconv"
	"time"

	"github.com/djylb/nps/lib/file"
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

// applyHealthReport takes an unhealthy target out of the rotation of the group's
// entry, or puts a healthy one back.
func applyHealthReport(id int, node string, rep healthcheck.Report) {
	if target := healthGroupTarget(id, node, rep.Group); target != nil {
		target.SetHealthy(rep.Addr, rep.Up)
	}
}

func tunnelTarget(t *file.Tunnel) *file.Target {
	t.RLock()
	defer t.RUnlock()
	return t.Target
}

func hostTarget(h *file.Host) *file.Target {
	h.RLock()
	defer h.RUnlock()
	return h.Target
}

func tunnelHealthGroup(id int) string { return "t" + strconv.Itoa(id) }
func hostHealthGroup(id int) string   { return "h" + strconv.Itoa(id) }

// healthGroupTarget returns the target that a report of group from node of
// client id is about, if the group is an entry of that client.
func healthGroupTarget(id int, node, group string) *file.Target {
	if len(group) < 2 {
		return nil
	}
	entry, err := strconv.Atoi(group[1:])
	if err != nil {
		return nil
	}
	switch group[0] {
	case 't':
		if t, err := file.GetDb().GetTask(entry); err == nil && t.Client != nil && t.Client.Id == id {
			return t.Routes.Target(node, tunnelTarget(t))
		}
	case 'h':
		if h, err := file.GetDb().GetHostById(entry); err == nil && h.Client != nil && h.Client.Id == id {
			return h.Routes.Target(node, hostTarget(h))
		}
	}
	return nil
}

// healthGroup is a health group as it should be on a node, or as it was sent.
type healthGroup struct {
	msg    healthcheck.Group
	target *file.Target
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
func newHealthGroup(group string, check file.HealthCheck, target *file.Target) (healthGroup, bool) {
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
func desiredHealthGroups(id int, node *Node, client *Client) map[string]healthGroup {
	groups := make(map[string]healthGroup)
	shared := client.healthNode() == node.UUID
	add := func(group string, check file.HealthCheck, routes *file.RouteSet, fallback *file.Target) {
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
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*file.Tunnel)
		v.RLock()
		mine := v.Client != nil && v.Client.Id == id && v.Status && (v.Mode == "tcp" || v.Mode == "udp" || v.Mode == "secret")
		target := v.Target
		v.RUnlock()
		if mine {
			add(tunnelHealthGroup(v.Id), v.HealthCheck(), v.Routes, target)
		}
		return true
	})
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*file.Host)
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
	c.nodes.Range(func(_, value any) bool {
		n, ok := value.(*Node)
		if ok && n.healthKick.Load() != nil && !n.IsOffline() && (first == "" || n.UUID < first) {
			first = n.UUID
		}
		return true
	})
	return first
}

// startHealthPush starts the one goroutine that sends health groups to peer and
// owns what was sent to it. npc forgets the groups with the
// connection, so a new connection starts empty and gets them all. It returns
// after the goroutine ended; since each write is bounded and fails once c is
// closed, stopping waits briefly at most.
func (s *Bridge) startHealthPush(id int, peer healthPeer, client *Client, node *Node) (stop func()) {
	kick := make(chan struct{}, 1)
	quit, done := make(chan struct{}), make(chan struct{})
	node.healthKick.Store(&kick)
	kickClientHealth(client) // who checks shared targets may have changed
	go func() {
		defer close(done)
		defer node.healthKick.CompareAndSwap(&kick, nil)
		sent := make(map[string]healthGroup)
		// The verdicts of this connection end with it.
		defer func() {
			for _, g := range sent {
				clearHealthVerdicts(g)
			}
		}()
		ticker := time.NewTicker(healthResync)
		defer ticker.Stop()
		for {
			if err := syncHealthGroups(id, peer, client, node, sent); err != nil {
				logs.Trace("send health groups to client %d: %v", id, err)
				return
			}
			select {
			case <-quit:
				return
			case <-kick:
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}

// syncHealthGroups makes what node checks through peer match the entries. A changed
// or removed group is deleted first; a changed one is then sent again.
func syncHealthGroups(id int, peer healthPeer, client *Client, node *Node, sent map[string]healthGroup) error {
	want := desiredHealthGroups(id, node, client)
	for name, old := range sent {
		if g, ok := want[name]; ok && g.same(old) {
			continue
		}
		if err := peer.DelHealthGroup(name); err != nil {
			return err
		}
		delete(sent, name)
		clearHealthVerdicts(old)
	}
	for name, g := range want {
		if _, ok := sent[name]; ok {
			continue
		}
		if err := peer.SetHealthGroup(g.msg); err != nil {
			return err
		}
		sent[name] = g
	}
	return nil
}

// clearHealthVerdicts puts back in the rotation what a group's checks took out,
// since nothing will report on those addresses any more.
func clearHealthVerdicts(g healthGroup) {
	for _, addr := range g.msg.Targets {
		g.target.SetHealthy(addr, true)
	}
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
	c.nodes.Range(func(_, value any) bool {
		if n, ok := value.(*Node); ok {
			if kick := n.healthKick.Load(); kick != nil {
				select {
				case *kick <- struct{}{}:
				default:
				}
			}
		}
		return true
	})
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for range ticker.C {
		s.pingClients()
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
