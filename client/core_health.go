package client

import (
	"context"
	"slices"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/netaddr"
)

// coreHealth runs the checks of the groups a session's snapshots list.
type coreHealth struct {
	hc     *HealthChecker
	stop   context.CancelFunc
	groups map[string]healthcheck.Group
}

// NewCoreHealth makes the runner of one session's health checks.
func NewCoreHealth(sink coreapi.HealthSink) coreapi.HealthRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &coreHealth{hc: NewHealthChecker(ctx, sink), stop: cancel, groups: map[string]healthcheck.Group{}}
}

func sameGroup(a, b healthcheck.Group) bool {
	return a.Type == b.Type && a.Path == b.Path && a.Interval == b.Interval && a.Timeout == b.Timeout &&
		a.Fall == b.Fall && slices.Equal(a.Targets, b.Targets)
}

// Set implements coreapi.HealthRunner: a group gone or changed is removed
// first, so a changed one starts anew.
func (h *coreHealth) Set(groups []healthcheck.Group) {
	want := make(map[string]healthcheck.Group, len(groups))
	for _, g := range groups {
		want[g.Name] = g
	}
	for name, old := range h.groups {
		if g, ok := want[name]; !ok || !sameGroup(g, old) {
			h.hc.delGroup(name)
			delete(h.groups, name)
		}
	}
	for name, g := range want {
		if _, ok := h.groups[name]; !ok {
			h.hc.setGroup(g)
			h.groups[name] = g
		}
	}
}

// ObserveDial implements coreapi.DialObserver: a failed dial has the target
// checked now, a TCP connection made postpones its TCP checks. A UDP dial
// reaches nothing, so it says nothing about the target.
func (h *coreHealth) ObserveDial(network, addr string, err error) {
	addr = netaddr.FormatAddress(addr)
	if err != nil {
		h.hc.dialFailed(addr)
	} else if network != coreapi.NetworkUDP {
		h.hc.touch(addr)
	}
}

// Stop implements coreapi.HealthRunner.
func (h *coreHealth) Stop() {
	h.hc.Stop()
	h.stop()
}
