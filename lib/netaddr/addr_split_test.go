package netaddr

import (
	"strconv"
	"testing"
)

// TestHostPortParseIP pins Host, Port and ParseIP on edge inputs. The
// columns old* record what the hand-written splitters they replaced
// returned; a row whose new value differs carries the reason.
func TestHostPortParseIP(t *testing.T) {
	tests := []struct {
		in string
		// host and port the old splitters gave, port "" for none
		oldHost, oldPort string
		// IP the old lenient parser gave, "" for none
		oldIP string
		// what Host, Port and ParseIP give now
		host string
		port int
		ip   string
		why  string
	}{
		{in: "", host: "", port: 0},
		{in: "1.2.3.4", oldHost: "1.2.3.4", oldIP: "1.2.3.4", host: "1.2.3.4", ip: "1.2.3.4"},
		{in: "1.2.3.4:80", oldHost: "1.2.3.4", oldPort: "80", oldIP: "1.2.3.4", host: "1.2.3.4", port: 80, ip: "1.2.3.4"},
		{in: "1.2.3.4:", oldHost: "1.2.3.4", oldIP: "1.2.3.4", host: "1.2.3.4"},
		{in: "1.2.3.4:abc", oldHost: "1.2.3.4", oldIP: "1.2.3.4", host: "1.2.3.4"},
		{in: "1.2.3.4:70000", oldHost: "1.2.3.4", oldIP: "1.2.3.4", host: "1.2.3.4"},
		{in: "example.com", oldHost: "example.com", host: "example.com"},
		{in: "Example.COM:443", oldHost: "Example.COM", oldPort: "443", host: "Example.COM", port: 443},
		{in: "example.com.:80", oldHost: "example.com.", oldPort: "80", host: "example.com.", port: 80},
		{in: "::1", oldHost: "::1", oldPort: "1", oldIP: "::1", host: "::1", ip: "::1", why: "a bare IPv6 address has no port"},
		{in: "2001:db8::8080", oldHost: "2001:db8::8080", oldPort: "8080", oldIP: "2001:db8::8080", host: "2001:db8::8080", ip: "2001:db8::8080", why: "a bare IPv6 address has no port"},
		{in: "[::1]", oldHost: "::1", oldIP: "::1", host: "::1"},
		{in: "[::1]:80", oldHost: "::1", oldPort: "80", oldIP: "::1", host: "::1", port: 80, ip: "::1"},
		{in: "[::1]:", oldHost: "::1", oldIP: "::1", host: "::1"},
		{in: "[::1"},
		{in: "[::1]junk", oldHost: "::1", oldIP: "::1", why: "text after the bracket is malformed"},
		{in: "[::1]:x", oldHost: "::1", oldIP: "::1", host: "::1"},
		{in: "[fe80::1%eth0]:80", oldHost: "fe80::1%eth0", oldPort: "80", oldIP: "fe80::1", host: "fe80::1%eth0", port: 80, ip: "fe80::1%eth0"},
		{in: "fe80::1%eth0", oldHost: "fe80::1%eth0", oldIP: "fe80::1", host: "fe80::1%eth0", ip: "fe80::1%eth0"},
		{in: "[::ffff:1.2.3.4]:80", oldHost: "::ffff:1.2.3.4", oldPort: "80", oldIP: "1.2.3.4", host: "::ffff:1.2.3.4", port: 80, ip: "::ffff:1.2.3.4"},
		{in: ":80", oldPort: "80", port: 80},
		{in: "a:b:c", oldHost: "a:b:c", host: "a:b:c"},
		{in: "host:80:90", oldHost: "host:80:90", oldPort: "90", host: "host:80:90", why: "too many colons: no port"},
		{in: " 1.2.3.4:80", oldHost: " 1.2.3.4", oldPort: "80", host: " 1.2.3.4", port: 80},
	}
	for _, tc := range tests {
		if got := Host(tc.in); got != tc.host {
			t.Errorf("Host(%q) = %q, want %q", tc.in, got, tc.host)
		}
		if got := Port(tc.in); got != tc.port {
			t.Errorf("Port(%q) = %d, want %d", tc.in, got, tc.port)
		}
		ip := ""
		if a, ok := ParseIP(tc.in); ok {
			ip = a.String()
		}
		if ip != tc.ip {
			t.Errorf("ParseIP(%q) = %q, want %q", tc.in, ip, tc.ip)
		}
		newPort := ""
		if tc.port != 0 {
			newPort = strconv.Itoa(tc.port)
		}
		if (tc.host != tc.oldHost || newPort != tc.oldPort) && tc.why == "" {
			t.Errorf("%q: differs from the old splitters without a reason", tc.in)
		}
	}
}
