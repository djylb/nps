package acl

import (
	"errors"
	"net/netip"
	"testing"
)

func ips(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

func TestCompileScopeMalformed(t *testing.T) {
	for _, text := range []string{"# only a comment\n\n", "  \n", "deny \n", "*.example.com", "ext:a.dat:x", "10.0.0.300/8"} {
		if _, err := CompileScope(text, nil); !errors.Is(err, ErrMalformedScope) {
			t.Errorf("%q: %v", text, err)
		}
	}
	if d, err := CompileScope("", nil); d != nil || err != nil {
		t.Fatalf("empty: %v %v", d, err)
	}
}

func TestScopeResolved(t *testing.T) {
	for _, tc := range []struct {
		name, scope, addr string
		ips               []netip.Addr
		want              bool
	}{
		{"mixed: denied name", "deny bad.example\nallow good.example", "bad.example:80", ips("1.1.1.1"), false},
		{"mixed: allowed name", "deny bad.example\nallow good.example", "good.example:80", ips("1.1.1.1"), true},
		{"mixed: neither", "deny bad.example\nallow good.example", "other.example:80", ips("1.1.1.1"), false},
		{"tab after verb", "deny\tbad.example\nallow\t 0.0.0.0/0", "bad.example:80", ips("1.1.1.1"), false},
		{"deny any address", "deny 10.0.0.0/8", "a.example:80", ips("1.1.1.1", "10.0.0.1"), false},
		{"deny other", "deny 10.0.0.0/8", "a.example:80", ips("1.1.1.1"), true},
		{"allow every address", "allow 10.0.0.0/8", "a.example:80", ips("10.0.0.2", "10.0.0.1"), true},
		{"allow not every address", "allow 10.0.0.0/8", "a.example:80", ips("10.0.0.2", "1.1.1.1"), false},
		{"allow name beats addresses", "allow a.example", "a.example:80", ips("1.1.1.1"), true},
		{"deny of ports", "deny 10.0.0.0/8:22", "a.example:80", ips("10.0.0.1"), true},
		{"deny of ports hit", "deny 10.0.0.0/8:22", "a.example:22", ips("10.0.0.1"), false},
		{"deny star: name", "deny *", "a.example:80", ips("1.1.1.1"), false},
		{"deny star: ip", "deny *", "[2001:db8::1]:80", ips("2001:db8::1"), false},
		{"allow star", "allow *\ndeny bad.example", "a.example:80", ips("1.1.1.1"), true},
		{"deny of NAT64 inner", "deny 127.0.0.0/8", "a.example:80", ips("64:ff9b::7f00:1"), false},
		{"deny of 6to4 inner", "deny 10.0.0.0/8", "a.example:80", ips("2002:a00:1::1"), false},
		{"allow needs inner too", "allow 64:ff9b::/96", "a.example:80", ips("64:ff9b::7f00:1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := CompileScope(tc.scope, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.AllowsResolved(tc.addr, tc.ips); got != tc.want {
				t.Fatalf("AllowsResolved = %t", got)
			}
		})
	}
	d, err := CompileScope("deny *", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.DeniesName("any.example:443") {
		t.Fatal("deny * needs no lookup")
	}
}

// Missing geodata does not block: deny does not match, allow matches.
func TestScopeGeodataMissingAllows(t *testing.T) {
	SetGeoData("", "")
	d, err := CompileScope("deny geoip:cn\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.AllowsResolved("a.example:80", ips("1.1.1.1")) {
		t.Fatal("deny without geodata refused")
	}
	d, err = CompileScope("allow geoip:cn\ndeny 1.1.1.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.AllowsResolved("a.example:80", ips("8.8.8.8")) || d.AllowsResolved("a.example:80", ips("1.1.1.1")) {
		t.Fatal("allow without geodata should match, deny still apply")
	}
}

func TestDestDecidesEmbeddedIPv4(t *testing.T) {
	for _, tc := range []struct{ ip, v4 string }{
		{"64:ff9b::7f00:1", "127.0.0.1"},
		{"64:ff9b:1:7f00:0:100::", "127.0.0.1"},
		{"2002:7f00:1::1", "127.0.0.1"},
		{"::7f00:1", "127.0.0.1"},
	} {
		got, ok := embeddedIPv4(netip.MustParseAddr(tc.ip))
		if !ok || got.String() != tc.v4 {
			t.Errorf("%s: %v %v, want %s", tc.ip, got, ok, tc.v4)
		}
	}
	for _, ip := range []string{"::1", "::", "::ffff:127.0.0.1", "2001:db8::1", "127.0.0.1"} {
		if _, ok := embeddedIPv4(netip.MustParseAddr(ip)); ok {
			t.Errorf("%s: embedded IPv4 found", ip)
		}
	}
}

func TestDestLiteralEmbeddedIPv4(t *testing.T) {
	d, err := CompileScope("deny 127.0.0.0/8", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allows("[64:ff9b::7f00:1]:80") || !d.Allows("[64:ff9b::808:808]:80") {
		t.Fatal("NAT64 literal not decided by its IPv4 address")
	}
}
