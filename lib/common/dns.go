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
}

func (d *preferredDNS) dial(ctx context.Context, network, system string) (net.Conn, error) {
	var dialer net.Dialer
	if d.reachable() {
		if c, err := dialer.DialContext(ctx, network, d.addr); err == nil {
			return c, nil
		}
	}
	return dialer.DialContext(ctx, network, system)
}

func (d *preferredDNS) reachable() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	recheck := dnsDownRecheck
	if d.up {
		recheck = dnsUpRecheck
	}
	if d.checked.IsZero() || time.Since(d.checked) >= recheck {
		d.up = d.probe()
		d.checked = time.Now()
	}
	return d.up
}

func (d *preferredDNS) probe() bool {
	c, err := net.DialTimeout("udp", d.addr, d.probeTimeout)
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
