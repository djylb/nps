package file

import "github.com/djylb/upstream"

// Route is where one request of a Tunnel or Host goes: to Target through
// Node, or through any node of the client if Node is empty.
type Route struct {
	Node   string
	Target *Target
}

// RouteSet holds the npc instances, by node UUID, that publish one
// configuration-mode entry, each with its own target. The settings of the
// entry other than the target are shared, the last publisher's being used.
// A nil RouteSet, as on entries managed on the web, has no routes.
type RouteSet struct {
	pool   *upstream.Pool[*Target]
	usable func(node string) bool
}

// NewRouteSet returns an empty set. usable tells whether a node can take
// connections now; nodes are skipped while it returns false.
func NewRouteSet(usable func(node string) bool) *RouteSet {
	return &RouteSet{
		pool:   upstream.New[*Target](upstream.Config{Balancer: upstream.RoundRobin(), Retry: upstream.RetryConfig{Attempts: 1 << 30}}),
		usable: usable,
	}
}

// Publish adds node with target, or gives it a new target.
func (r *RouteSet) Publish(node string, target *Target) {
	_ = r.pool.Put(upstream.Target[*Target]{ID: node, Weight: 1, Value: target})
}

// Retain removes the nodes keep rejects and reports whether it removed any.
func (r *RouteSet) Retain(keep func(node string) bool) (removed bool) {
	for _, b := range r.pool.Backends() {
		if !keep(b.ID()) {
			r.pool.Remove(b.ID())
			removed = true
		}
	}
	return removed
}

// Len returns the number of nodes.
func (r *RouteSet) Len() int {
	if r == nil {
		return 0
	}
	return r.pool.Len()
}

// HasOther reports whether a node other than node is in the set.
func (r *RouteSet) HasOther(node string) bool {
	if r == nil {
		return false
	}
	for _, b := range r.pool.Backends() {
		if b.ID() != node {
			return true
		}
	}
	return false
}

// Target returns the target of node, or fallback if node has none.
func (r *RouteSet) Target(node string, fallback *Target) *Target {
	if r != nil {
		if b, ok := r.pool.Backend(node); ok {
			return b.Value()
		}
	}
	return fallback
}

// Holding returns target if a node of the set has it, and otherwise the
// target of the first node.
func (r *RouteSet) Holding(target *Target) *Target {
	backends := r.pool.Backends()
	for _, b := range backends {
		if b.Value() == target {
			return target
		}
	}
	if len(backends) > 0 {
		return backends[0].Value()
	}
	return target
}

// pick returns the next usable node in round-robin order. With no usable
// node it returns the first one, whose link then fails: a request is never
// sent to an instance that does not publish the entry. A set without nodes
// leaves the choice of the node to the client.
func (r *RouteSet) pick(fallback *Target) Route {
	if r.Len() == 0 {
		return Route{Target: fallback}
	}
	req := r.pool.Request("")
	for {
		l, err := req.Next()
		if err != nil {
			break
		}
		node, target := l.Backend().ID(), l.Value()
		l.Release(upstream.Neutral)
		if r.usable(node) {
			return Route{Node: node, Target: target}
		}
	}
	if backends := r.pool.Backends(); len(backends) > 0 {
		return Route{Node: backends[0].ID(), Target: backends[0].Value()}
	}
	return Route{Target: fallback}
}

// Route picks the route of one request of t.
func (t *Tunnel) Route() Route {
	t.RLock()
	fallback := t.Target
	t.RUnlock()
	return t.Routes.pick(fallback)
}

// Route picks the route of one request of s.
func (s *Host) Route() Route {
	s.RLock()
	fallback := s.Target
	s.RUnlock()
	return s.Routes.pick(fallback)
}
