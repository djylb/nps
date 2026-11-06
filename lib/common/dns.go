package common

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

var customDnsAddr string

func SetCustomDNS(dnsAddr string) {
	if dnsAddr == "" {
		return
	}
	colonCount := strings.Count(dnsAddr, ":")
	if colonCount == 0 {
		dnsAddr += ":53"
	} else if colonCount > 1 && !strings.Contains(dnsAddr, "]:") {
		if strings.Contains(dnsAddr, "]") {
			dnsAddr += ":53"
		} else {
			dnsAddr = "[" + dnsAddr + "]:53"
		}
	}

	customDnsAddr = dnsAddr

	preferred := &preferredDNS{addr: dnsAddr, probeTimeout: dnsProbeTimeout}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial:     preferred.dial,
	}
}

const (
	dnsProbeTimeout = 2 * time.Second
	dnsUpRecheck    = 30 * time.Second
	dnsDownRecheck  = 10 * time.Second
)

// dnsProbeQuery asks for the NS records of the root zone.
var dnsProbeQuery = []byte{0x6e, 0x70, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 1}

// systemResolver looks up the name of a dns_server given as a domain, which
// the resolver that dns_server itself feeds cannot do.
var systemResolver = &net.Resolver{}

// preferredDNS sends lookups to addr while it answers and otherwise to the
// system's servers, which the Go resolver passes to its Dial function. A UDP
// dial cannot tell that a server is unreachable and a resolver timeout costs
// seconds per query, so reachability comes from a cached probe. Any answer
// counts as reachable: a name addr does not know is not looked up elsewhere.
type preferredDNS struct {
	addr         string
	probeTimeout time.Duration

	mu      sync.Mutex
	up      bool
	checked time.Time
	probing chan struct{} // closed when the running probe ends; nil without one
}

func (d *preferredDNS) dial(ctx context.Context, network, system string) (net.Conn, error) {
	dialer := net.Dialer{Resolver: systemResolver}
	if d.reachable(ctx) {
		if c, err := dialer.DialContext(ctx, network, d.addr); err == nil {
			return c, nil
		}
	}
	return dialer.DialContext(ctx, network, system)
}

// reachable returns the cached result while it is fresh. Otherwise it waits
// for a probe, shared by all lookups, until ctx ends; then the last result
// stands.
func (d *preferredDNS) reachable(ctx context.Context) bool {
	d.mu.Lock()
	recheck := dnsDownRecheck
	if d.up {
		recheck = dnsUpRecheck
	}
	if !d.checked.IsZero() && time.Since(d.checked) < recheck {
		defer d.mu.Unlock()
		return d.up
	}
	done := d.probing
	if done == nil {
		done = make(chan struct{})
		d.probing = done
		go func() {
			up := d.probe()
			d.mu.Lock()
			d.up, d.checked, d.probing = up, time.Now(), nil
			d.mu.Unlock()
			close(done)
		}()
	}
	d.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.up
}

func (d *preferredDNS) probe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), d.probeTimeout)
	defer cancel()
	dialer := net.Dialer{Resolver: systemResolver}
	c, err := dialer.DialContext(ctx, "udp", d.addr)
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(d.probeTimeout))
	if _, err := c.Write(dnsProbeQuery); err != nil {
		return false
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	return err == nil && n >= 12 && buf[0] == dnsProbeQuery[0] && buf[1] == dnsProbeQuery[1]
}

func GetCustomDNS() string {
	if customDnsAddr != "" {
		return customDnsAddr
	}
	return "8.8.8.8:53"
}
