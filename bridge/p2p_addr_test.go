package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/holepunch/protocol"
)

// testAnnouncer returns an announcer with fixed lookups, detection and
// egress addresses.
func testAnnouncer(p2pIP string, names map[string][]string, detect4, detect6, egress string) *p2pAnnouncer {
	a := newP2PAnnouncer(p2pIP)
	a.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, v := range names[host] {
			out = append(out, netip.MustParseAddr(v))
		}
		if len(out) == 0 {
			return nil, errors.New("no such host")
		}
		return out, nil
	}
	a.detect = func(w netip.Addr) netip.Addr {
		v := detect4
		if w.Is6() {
			v = detect6
		}
		ip, _ := netip.ParseAddr(v)
		return ip
	}
	a.egress = func(netip.Addr) netip.Addr {
		ip, _ := netip.ParseAddr(egress)
		return ip
	}
	return a
}

func udp(s string) net.Addr { return net.UDPAddrFromAddrPort(netip.MustParseAddrPort(s)) }

func addrs(v ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range v {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestP2PCandidates(t *testing.T) {
	names := map[string][]string{"nps.example": {"1.1.1.1", "2606:4700::1111"}}
	cases := []struct {
		name     string
		a        *p2pAnnouncer
		reported string
		trusted  bool
		client   string
		local    string
		want     []netip.Addr
	}{
		{
			name:     "explicit p2p_ip first, then the reported, local, egress and detected addresses",
			a:        testAnnouncer("9.9.9.9,::", nil, "8.8.4.4", "2001:4860::8844", "8.8.8.8"),
			reported: "1.0.0.1", trusted: true, client: "4.4.4.4:5000", local: "9.9.9.10:8024",
			want: addrs("9.9.9.9", "1.0.0.1", "9.9.9.10", "8.8.8.8", "2001:4860::8844"),
		},
		{
			name:     "private candidates go last for a public client",
			a:        testAnnouncer("", nil, "192.168.1.2", "", "10.0.0.5"),
			reported: "1.0.0.1", trusted: true, client: "4.4.4.4:5000", local: "10.0.0.5:8024",
			want: addrs("1.0.0.1", "10.0.0.5", "192.168.1.2"),
		},
		{
			name:     "an untrusted report is ignored",
			a:        testAnnouncer("", nil, "8.8.4.4", "", ""),
			reported: "1.0.0.1", client: "4.4.4.4:5000", local: "9.9.9.10:8024",
			want: addrs("9.9.9.10", "8.8.4.4"),
		},
		{
			name:     "automatic loopback and link-local addresses are dropped, zones too",
			a:        testAnnouncer("", nil, "127.0.0.1", "fe80::1%eth0", "127.0.0.1"),
			reported: "127.0.0.1", trusted: true, client: "4.4.4.4:5000", local: "[fe80::2%eth0]:8024",
			want: nil,
		},
		{
			name:   "an explicit loopback address is kept",
			a:      testAnnouncer("127.0.0.1", nil, "8.8.4.4", "", ""),
			client: "127.0.0.1:5000", local: "127.0.0.1:8024",
			want: addrs("127.0.0.1"),
		},
		{
			name:   "private candidates keep their place for a client on the LAN",
			a:      testAnnouncer("", nil, "8.8.4.4", "", ""),
			client: "192.168.1.9:5000", local: "192.168.1.2:8024",
			want: addrs("192.168.1.2", "8.8.4.4"),
		},
		{
			name:   "a name gives both families",
			a:      testAnnouncer("nps.example", names, "8.8.4.4", "2001:4860::8844", ""),
			client: "4.4.4.4:5000", local: "9.9.9.10:8024",
			want: addrs("1.1.1.1", "2606:4700::1111", "9.9.9.10"),
		},
		{
			name:   "an unresolved name falls through to the next sources",
			a:      testAnnouncer("missing.example", names, "8.8.4.4", "2001:4860::8844", ""),
			client: "4.4.4.4:5000", local: "9.9.9.10:8024",
			want: addrs("9.9.9.10"),
		},
		{
			name:     "0.0.0.0 serves IPv4 only",
			a:        testAnnouncer("0.0.0.0", nil, "8.8.4.4", "2001:4860::8844", ""),
			reported: "2606:4700::1", trusted: true, client: "[2606:4700::2]:5000", local: "[2606:4700::3]:8024",
			want: addrs("8.8.4.4"),
		},
		{
			name:   "an empty p2p_ip detects both families",
			a:      testAnnouncer("", nil, "8.8.4.4", "2001:4860::8844", ""),
			client: "4.4.4.4:5000",
			want:   addrs("8.8.4.4", "2001:4860::8844"),
		},
	}
	for _, tc := range cases {
		var local net.Addr
		if tc.local != "" {
			local = udp(tc.local)
		}
		got := tc.a.candidates(context.Background(), tc.reported, tc.trusted, udp(tc.client), local)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.name, got, tc.want)
		}
	}
}

func TestP2PResolveFailureIsCached(t *testing.T) {
	a := testAnnouncer("nps.example", nil, "", "", "")
	lookups := 0
	a.lookup = func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		return nil, errors.New("no such host")
	}
	for range 3 {
		a.configured(context.Background())
	}
	if lookups != 1 {
		t.Fatalf("%d lookups of a failing name within the retry interval, want 1", lookups)
	}
}

// TestP2PSpecAt checks that a client probes the observers at every
// candidate of a served family, the first candidate in the first observer
// group, and no observer of a family without a candidate.
func TestP2PSpecAt(t *testing.T) {
	svc := newTestP2PBridge(t).p2p.Load()
	ipv6 := len(svc.probe.ProbeSpecAt(netip.IPv6Loopback()).Observers) > 0 // false on a host without IPv6
	for _, cands := range [][]netip.Addr{
		addrs("1.1.1.1", "8.8.8.8", "2606:4700::1111"),
		addrs("1.1.1.1"),
		nil,
	} {
		spec := svc.specAt(cands...)
		var got []netip.Addr
		for _, o := range spec.Observers { // ordered by family, then id
			if a := o.Addr.Addr(); !slices.Contains(got, a) {
				got = append(got, a)
			}
		}
		var want []netip.Addr
		for _, c := range cands {
			if c.Is4() || ipv6 {
				want = append(want, c)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("candidates %v: observers %s, want them at %v", cands, fmtObservers(spec.Observers), want)
		}
	}
}

func fmtObservers(obs []protocol.ObserverSpec) string {
	var out []string
	for _, o := range obs {
		out = append(out, fmt.Sprintf("%d@%s", o.ID, o.Addr))
	}
	return strings.Join(out, " ")
}
