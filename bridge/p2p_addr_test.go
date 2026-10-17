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

	"github.com/djylb/holepunch/natprobe"
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
			name:     "explicit p2p_ip first, then the public egress and local addresses before the reported one, detected",
			a:        testAnnouncer("9.9.9.9,::", nil, "8.8.4.4", "2001:4860::8844", "8.8.8.8"),
			reported: "1.0.0.1", trusted: true, client: "4.4.4.4:5000", local: "9.9.9.10:8024",
			want: addrs("9.9.9.9", "8.8.8.8", "9.9.9.10", "1.0.0.1", "2001:4860::8844"),
		},
		{
			name:     "behind a NAT the reported address comes before the private local one",
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
		got := tc.a.candidates(context.Background(), tc.reported, tc.trusted, udp(tc.client), local).addrs
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

func TestP2PSelectObservers(t *testing.T) {
	obs := func(group uint8, id uint8, addr string) protocol.ObserverSpec {
		return protocol.ObserverSpec{ID: id, Group: group, Addr: netip.MustParseAddrPort(addr)}
	}
	wildcard := []natprobe.ObserverGroup{
		{Bind: netip.IPv4Unspecified(), Advertise: p2pPublicHint4},
		{Bind: netip.IPv6Unspecified(), Advertise: p2pPublicHint6},
	}
	all := []protocol.ObserverSpec{obs(0, 0, "203.0.113.1:6000"), obs(0, 1, "203.0.113.1:6001"), obs(1, 8, "[2001:db8::1]:6000")}
	dual := selectObservers(all, wildcard, p2pCandidates{addrs: addrs("1.1.1.1", "8.8.8.8", "2606:4700::1111")})
	if got := fmtObservers(dual); got != "0@1.1.1.1:6000 1@1.1.1.1:6001 8@[2606:4700::1111]:6000" {
		t.Fatalf("dual stack = %s", got)
	}
	if got := fmtObservers(selectObservers(all, wildcard, p2pCandidates{addrs: addrs("1.1.1.1")})); got != "0@1.1.1.1:6000 1@1.1.1.1:6001" {
		t.Fatalf("IPv4 only = %s", got)
	}

}

func fmtObservers(obs []protocol.ObserverSpec) string {
	var out []string
	for _, o := range obs {
		out = append(out, fmt.Sprintf("%d@%s", o.ID, o.Addr))
	}
	return strings.Join(out, " ")
}

func TestP2PScopeHint(t *testing.T) {
	if got := p2pScopeHint(netip.IPv4Unspecified(), "1.1.1.1"); got != p2pPublicHint4 {
		t.Errorf("public IPv4 hint = %s", got)
	}
	if got := p2pScopeHint(netip.IPv4Unspecified(), "192.168.1.2,::"); got != p2pPrivateHint4 {
		t.Errorf("private IPv4 hint = %s", got)
	}
	if got := p2pScopeHint(netip.IPv6Unspecified(), "fd12::1"); got != p2pPrivateHint6 {
		t.Errorf("private IPv6 hint = %s", got)
	}
}
