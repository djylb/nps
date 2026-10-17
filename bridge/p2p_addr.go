package bridge

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

const (
	// p2pResolveTimeout bounds the lookup of a host name in p2p_ip when a
	// session is created.
	p2pResolveTimeout = 250 * time.Millisecond
	// p2pResolveTTL is how long a lookup is reused; short, so that a name
	// behind dynamic DNS follows its changes.
	p2pResolveTTL = time.Minute
	// p2pResolveRetry is how long a failed lookup is not repeated.
	p2pResolveRetry = 30 * time.Second
)

// p2pAnnouncer lists the addresses each client probes the observers at, in
// order of preference:
//
//  1. the explicit addresses of p2p_ip (IPs or host names, comma separated;
//     a name gives its A and AAAA records),
//  2. the server address the client itself connected to, when it reports
//     one and reached the bridge directly (not through a WebSocket proxy),
//  3. the local address of the client's control connection on this server
//     and the source address this host sends to the client from,
//  4. the host's outbound address for the wildcards of p2p_ip, asked again
//     for every session: IPv4 for 0.0.0.0, IPv6 for ::.
//
// Automatic candidates that are loopback or link-local are dropped.
//
// The families are those p2p_ip names (by address, wildcard or resolved
// name), both when p2p_ip is empty, which detects both. Every candidate is
// announced, as its own observer group, up to natprobe's limits, which keep
// the first: a client probes them all and an unreachable one costs nothing
// but its probes. For a client at a public address, candidates that are not
// public (common.IsPublicIPStrict) go last: behind a port mapping or a 1:1
// NAT they are this host's view, not the client's.
type p2pAnnouncer struct {
	p2pIP  string
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	detect func(wildcard netip.Addr) netip.Addr // the host's outbound address of the family of 0.0.0.0 or ::
	egress func(client netip.Addr) netip.Addr   // the source address this host sends to client from

	mu    sync.Mutex
	cache map[string]p2pResolved // by host name of p2p_ip
}

type p2pResolved struct {
	addrs  []netip.Addr // the last addresses found
	at     time.Time
	failed bool // the last lookup failed
}

func newP2PAnnouncer(p2pIP string) *p2pAnnouncer {
	return &p2pAnnouncer{
		p2pIP: p2pIP,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		detect: func(wildcard netip.Addr) netip.Addr {
			ip := common.GetOutboundIPv4()
			if wildcard.Is6() {
				ip = common.GetOutboundIPv6()
			}
			a, _ := netip.AddrFromSlice(ip)
			return a.Unmap()
		},
		egress: func(client netip.Addr) netip.Addr {
			if !client.IsValid() {
				return netip.Addr{}
			}
			a, _ := netip.AddrFromSlice(common.PickEgressIPFor(client.AsSlice()))
			return a.Unmap()
		},
		cache: make(map[string]p2pResolved),
	}
}

// configured returns the explicit addresses of p2p_ip, resolving host names
// with a short timeout (a failed lookup keeps the last addresses it found),
// its wildcards and the families it names; an empty p2p_ip is both
// wildcards. A p2p_ip that names no family at all (an unresolved name) allows
// both.
func (a *p2pAnnouncer) configured(ctx context.Context) (addrs, wildcards []netip.Addr, families [2]bool) {
	entries := common.HandleArrEmptyVal(strings.Split(a.p2pIP, ","))
	if len(entries) == 0 {
		return nil, []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()}, [2]bool{true, true}
	}
	for _, v := range entries {
		v = strings.Trim(strings.TrimSpace(v), "[]")
		if ip, err := netip.ParseAddr(v); err == nil {
			if ip = ip.Unmap(); ip.IsUnspecified() {
				wildcards = append(wildcards, ip)
			} else {
				addrs = append(addrs, ip)
			}
			families[p2pFamily(ip)] = true
			continue
		}
		for _, ip := range a.resolve(ctx, v) {
			addrs = append(addrs, ip)
			families[p2pFamily(ip)] = true
		}
	}
	if !families[0] && !families[1] {
		families = [2]bool{true, true}
	}
	return addrs, wildcards, families
}

func (a *p2pAnnouncer) resolve(ctx context.Context, host string) []netip.Addr {
	a.mu.Lock()
	cached, ok := a.cache[host]
	a.mu.Unlock()
	switch {
	case ok && cached.failed && time.Since(cached.at) < p2pResolveRetry:
		return cached.addrs
	case ok && !cached.failed && time.Since(cached.at) < p2pResolveTTL:
		return cached.addrs
	}
	lctx, cancel := context.WithTimeout(ctx, p2pResolveTimeout)
	addrs, err := a.lookup(lctx, host)
	cancel()
	if err != nil || len(addrs) == 0 {
		// Warned once per p2pResolveRetry: the failure is cached as long.
		logs.Warn("P2P: cannot resolve p2p_ip %s: %v; using the last addresses found and the next address sources", host, err)
		a.mu.Lock()
		a.cache[host] = p2pResolved{addrs: cached.addrs, at: time.Now(), failed: true}
		a.mu.Unlock()
		return cached.addrs
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap().WithZone("")
	}
	a.mu.Lock()
	a.cache[host] = p2pResolved{addrs: addrs, at: time.Now()}
	a.mu.Unlock()
	return addrs
}

// detected returns the host's outbound addresses for the wildcards, each of
// the wildcard's family.
func (a *p2pAnnouncer) detected(wildcards []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, w := range wildcards {
		if d := a.detect(w); d.IsValid() && p2pFamily(d) == p2pFamily(w) {
			out = append(out, d)
		}
	}
	return out
}

// candidates lists the addresses one client may probe the observers at, in
// order of preference. reported is the server address the client says it
// connected to, used when trusted; client and local are the addresses of its
// control connection as this server sees them. Loopback and link-local
// addresses are kept from p2p_ip alone, where the operator wrote them.
func (a *p2pAnnouncer) candidates(ctx context.Context, reported string, trusted bool, client, local net.Addr) []netip.Addr {
	configured, wildcards, families := a.configured(ctx)
	clientIP := addrIP(client)
	var auto []netip.Addr
	if ip, err := netip.ParseAddr(reported); trusted && err == nil {
		auto = append(auto, ip)
	}
	auto = append(auto, addrIP(local), a.egress(clientIP))
	auto = append(auto, a.detected(wildcards)...)
	return orderCandidates(configured, auto, clientIP, families)
}

// orderCandidates joins the explicit and the automatic candidates, drops
// unusable and repeated addresses, those of other families than allowed
// and, among the automatic ones, loopback and link-local addresses, which
// no other host reaches; zones are dropped. For a client at a public
// address the candidates that are not public go last, keeping the order
// otherwise.
func orderCandidates(explicit, auto []netip.Addr, client netip.Addr, families [2]bool) []netip.Addr {
	var near, far []netip.Addr
	seen := make(map[netip.Addr]bool)
	clientPublic := p2pPublic(client)
	add := func(c netip.Addr, automatic bool) {
		c = c.Unmap().WithZone("")
		switch {
		case !c.IsValid() || c.IsUnspecified() || c.IsMulticast() || seen[c] || !families[p2pFamily(c)]:
			return
		case automatic && (c.IsLoopback() || c.IsLinkLocalUnicast()):
			return
		}
		seen[c] = true
		if clientPublic && !p2pPublic(c) {
			far = append(far, c)
		} else {
			near = append(near, c)
		}
	}
	for _, c := range explicit {
		add(c, false)
	}
	for _, c := range auto {
		add(c, true)
	}
	return append(near, far...)
}

// p2pPublic reports whether a is a public address.
func p2pPublic(a netip.Addr) bool {
	return a.IsValid() && common.IsPublicIPStrict(a.AsSlice())
}

func addrIP(a net.Addr) netip.Addr {
	if a == nil {
		return netip.Addr{}
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap().WithZone("")
}

// p2pFamily is 0 for IPv4 and 1 for IPv6.
func p2pFamily(a netip.Addr) int {
	if a.Unmap().Is4() {
		return 0
	}
	return 1
}
