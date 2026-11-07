package netaddr

import (
	"net"
	"testing"
)

func TestSplitServerAndPath(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantServer string
		wantPath   string
	}{
		{name: "with path", input: "example.com/api", wantServer: "example.com", wantPath: "/api"},
		{name: "without path", input: "example.com", wantServer: "example.com", wantPath: ""},
		{name: "path only", input: "/api", wantServer: "", wantPath: "/api"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, path := SplitServerAndPath(tc.input)
			if server != tc.wantServer || path != tc.wantPath {
				t.Fatalf("SplitServerAndPath(%q) = (%q, %q), want (%q, %q)", tc.input, server, path, tc.wantServer, tc.wantPath)
			}
		})
	}
}

func TestHostAndPortHelpers(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		extract    string
		removePort string
		ip         string
		port       int
	}{
		{name: "domain with path", input: "example.com:8080/path", extract: "example.com:8080", removePort: "example.com", ip: "example.com", port: 8080},
		{name: "url with domain", input: "https://example.com:8443/api", extract: "example.com:8443", removePort: "example.com", ip: "example.com", port: 8443},
		{name: "ipv6 address", input: "[2001:db8::1]:443", extract: "[2001:db8::1]:443", removePort: "[2001:db8::1]", ip: "2001:db8::1", port: 443},
		{name: "invalid ipv6", input: "[2001:db8::1", extract: "[2001:db8::1", removePort: "", ip: "", port: 0},
		{name: "without port", input: "localhost", extract: "localhost", removePort: "localhost", ip: "localhost", port: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractHost(tc.input); got != tc.extract {
				t.Fatalf("ExtractHost(%q) = %q, want %q", tc.input, got, tc.extract)
			}
			if got := RemovePortFromHost(tc.extract); got != tc.removePort {
				t.Fatalf("RemovePortFromHost(%q) = %q, want %q", tc.extract, got, tc.removePort)
			}
			if got := GetIpByAddr(tc.extract); got != tc.ip {
				t.Fatalf("GetIpByAddr(%q) = %q, want %q", tc.extract, got, tc.ip)
			}
			if got := getPortByAddr(tc.extract); got != tc.port {
				t.Fatalf("GetPortByAddr(%q) = %d, want %d", tc.extract, got, tc.port)
			}
		})
	}
}

func TestSplitAddrAndHost(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantAddr     string
		wantHost     string
		wantSNI      string
		wantPortText string
	}{
		{name: "no separator", input: "example.com:443", wantAddr: "example.com:443", wantHost: "example.com:443", wantSNI: "example.com", wantPortText: "443"},
		{name: "explicit host", input: "127.0.0.1:8080@example.com:443", wantAddr: "127.0.0.1:8080", wantHost: "example.com:443", wantSNI: "example.com", wantPortText: "443"},
		{name: "empty host fallback", input: "127.0.0.1:8080@", wantAddr: "127.0.0.1:8080", wantHost: "127.0.0.1:8080", wantSNI: "", wantPortText: "8080"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, host, sni := SplitAddrAndHost(tc.input)
			if addr != tc.wantAddr || host != tc.wantHost || sni != tc.wantSNI {
				t.Fatalf("SplitAddrAndHost(%q) = (%q, %q, %q), want (%q, %q, %q)", tc.input, addr, host, sni, tc.wantAddr, tc.wantHost, tc.wantSNI)
			}
			if got := GetPortStrByAddr(host); got != tc.wantPortText {
				t.Fatalf("GetPortStrByAddr(%q) = %q, want %q", host, got, tc.wantPortText)
			}
		})
	}
}

func TestPortHelpers(t *testing.T) {
	ports := GetPorts(" 80, 443, 1000-1002,1002-1001,invalid,0,65537")
	want := []int{80, 443, 1000, 1001, 1002}
	if len(ports) != len(want) {
		t.Fatalf("GetPorts() length = %d, want %d (%v)", len(ports), len(want), ports)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("GetPorts() = %v, want %v", ports, want)
		}
	}

	if got := FormatAddress("8080"); got != "127.0.0.1:8080" {
		t.Fatalf("FormatAddress(port) = %q, want %q", got, "127.0.0.1:8080")
	}
	if got := FormatAddress("127.0.0.1:8080"); got != "127.0.0.1:8080" {
		t.Fatalf("FormatAddress(addr) = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestIPAndBindHelpers(t *testing.T) {
	v4 := NormalizeIP(net.ParseIP("192.0.2.1"))
	if v4 == nil || v4.To4() == nil {
		t.Fatalf("NormalizeIP(v4) = %v, want ipv4", v4)
	}
	v6 := NormalizeIP(net.ParseIP("2001:db8::1"))
	if v6 == nil || v6.To16() == nil || v6.To4() != nil {
		t.Fatalf("NormalizeIP(v6) = %v, want ipv6", v6)
	}

	if !IsZeroIP(nil) || !IsZeroIP(net.IPv4zero) || !IsZeroIP(net.IPv6zero) || IsZeroIP(net.ParseIP("127.0.0.1")) {
		t.Fatalf("IsZeroIP() returned unexpected result")
	}

	if ip := ParseIPFromAddr("[fe80::1%eth0]:8080"); ip == nil || ip.String() != "fe80::1" {
		t.Fatalf("ParseIPFromAddr(v6 with zone) = %v, want fe80::1", ip)
	}
	if ip := ParseIPFromAddr("not-an-addr"); ip != nil {
		t.Fatalf("ParseIPFromAddr(invalid) = %v, want nil", ip)
	}

	if got := BuildTCPBindAddr("127.0.0.1"); got == nil {
		t.Fatalf("BuildTCPBindAddr(valid) = nil, want non-nil")
	}
	if got := BuildTCPBindAddr("bad-ip"); got != nil {
		t.Fatalf("BuildTCPBindAddr(invalid) = %v, want nil", got)
	}
	if got := BuildUDPBindAddr("2001:db8::1"); got == nil {
		t.Fatalf("BuildUDPBindAddr(valid) = nil, want non-nil")
	}
	if got := BuildUDPBindAddr("bad-ip"); got != nil {
		t.Fatalf("BuildUDPBindAddr(invalid) = %v, want nil", got)
	}

	if !IsPublicHost("8.8.8.8:53") || IsPublicHost("127.0.0.1:53") || !IsPublicHost("example.com:443") || IsPublicHost(":bad") {
		t.Fatalf("IsPublicHost() returned unexpected result")
	}
}
