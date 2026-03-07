package common

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"
)

func TestValidateAddr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid ipv4", input: "127.0.0.1:80", want: "127.0.0.1:80"},
		{name: "valid ipv6", input: "[2001:db8::1]:443", want: "[2001:db8::1]:443"},
		{name: "domain not allowed", input: "example.com:443", want: ""},
		{name: "invalid port", input: "127.0.0.1:70000", want: ""},
		{name: "missing port", input: "127.0.0.1", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidateAddr(tc.input); got != tc.want {
				t.Fatalf("ValidateAddr(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

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

func TestMathHelpers(t *testing.T) {
	if got := Max(-3, 0, 10, 2); got != 10 {
		t.Fatalf("Max() = %d, want %d", got, 10)
	}
	if got := Min(-3, 0, 10, 2); got != -3 {
		t.Fatalf("Min() = %d, want %d", got, -3)
	}

	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "positive in range", input: 8080, want: 8080},
		{name: "positive overflow", input: 70000, want: 4464},
		{name: "negative", input: -1, want: 65535},
		{name: "negative large", input: -70000, want: 61072},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetPort(tc.input); got != tc.want {
				t.Fatalf("GetPort(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestDomainCheck(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{name: "plain domain", input: "example.com", valid: true},
		{name: "http domain", input: "http://example.com", valid: true},
		{name: "https domain with path", input: "https://example.com/path", valid: true},
		{name: "invalid ip", input: "127.0.0.1", valid: false},
		{name: "invalid string", input: "not_a_domain", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DomainCheck(tc.input); got != tc.valid {
				t.Fatalf("DomainCheck(%q) = %v, want %v", tc.input, got, tc.valid)
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
			if got := GetPortByAddr(tc.extract); got != tc.port {
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

func TestBuildAddress(t *testing.T) {
	if got := BuildAddress("127.0.0.1", "80"); got != "127.0.0.1:80" {
		t.Fatalf("BuildAddress IPv4 = %q, want %q", got, "127.0.0.1:80")
	}
	if got := BuildAddress("2001:db8::1", "443"); got != "[2001:db8::1]:443" {
		t.Fatalf("BuildAddress IPv6 = %q, want %q", got, "[2001:db8::1]:443")
	}
}

func TestGetWriteStr(t *testing.T) {
	got := GetWriteStr("alpha", "beta")
	want := []byte("alpha" + CONN_DATA_SEQ + "beta" + CONN_DATA_SEQ)
	if !bytes.Equal(got, want) {
		t.Fatalf("GetWriteStr() = %q, want %q", string(got), string(want))
	}
}

func TestBinaryWrite(t *testing.T) {
	raw := bytes.NewBuffer(nil)
	BinaryWrite(raw, "info", "true")

	buf := raw.Bytes()
	if len(buf) < 4 {
		t.Fatalf("BinaryWrite() output too short: %d", len(buf))
	}

	payloadLen := int(binary.LittleEndian.Uint32(buf[:4]))
	payload := buf[4:]
	if payloadLen != len(payload) {
		t.Fatalf("payload length = %d, want %d", payloadLen, len(payload))
	}

	want := []byte("info" + CONN_DATA_SEQ + "true" + CONN_DATA_SEQ)
	if !bytes.Equal(payload, want) {
		t.Fatalf("payload = %q, want %q", string(payload), string(want))
	}
}

func TestArrayAndPortHelpers(t *testing.T) {
	if !InStrArr([]string{"a", "b"}, "b") || InStrArr([]string{"a", "b"}, "c") {
		t.Fatalf("InStrArr() returned unexpected result")
	}
	if !InIntArr([]int{1, 2, 3}, 2) || InIntArr([]int{1, 2, 3}, 4) {
		t.Fatalf("InIntArr() returned unexpected result")
	}

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

	if !IsPort("65536") || IsPort("65537") || IsPort("0") || IsPort("bad") {
		t.Fatalf("IsPort() returned unexpected result")
	}
	if got := FormatAddress("8080"); got != "127.0.0.1:8080" {
		t.Fatalf("FormatAddress(port) = %q, want %q", got, "127.0.0.1:8080")
	}
	if got := FormatAddress("127.0.0.1:8080"); got != "127.0.0.1:8080" {
		t.Fatalf("FormatAddress(addr) = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestSliceAndMapHelpers(t *testing.T) {
	trimmed := TrimArr([]string{" a ", "", "  ", "b"})
	if len(trimmed) != 2 || trimmed[0] != "a" || trimmed[1] != "b" {
		t.Fatalf("TrimArr() = %v, want [a b]", trimmed)
	}

	if !IsArrContains([]string{"x", "y"}, "y") || IsArrContains(nil, "x") {
		t.Fatalf("IsArrContains() returned unexpected result")
	}

	removed := RemoveArrVal([]string{"a", "b", "c"}, "b")
	if len(removed) != 2 || removed[0] != "a" || removed[1] != "c" {
		t.Fatalf("RemoveArrVal() = %v, want [a c]", removed)
	}

	handled := HandleArrEmptyVal([]string{"a", "", " c ", " "})
	if len(handled) != 3 || handled[0] != "a" || handled[1] != "a" || handled[2] != "c" {
		t.Fatalf("HandleArrEmptyVal() = %v, want [a a c]", handled)
	}

	a1 := []string{"x"}
	a2 := []string{"m", "n", "o"}
	a3 := []string{}
	max := ExtendArrs(&a1, &a2, &a3)
	if max != 3 {
		t.Fatalf("ExtendArrs() max = %d, want 3", max)
	}
	if len(a1) != 3 || a1[2] != "x" || len(a3) != 3 || a3[0] != "" {
		t.Fatalf("ExtendArrs() unexpected arrays: a1=%v a3=%v", a1, a3)
	}

	if got := BytesToNum([]byte{1, 2, 3}); got != 123 {
		t.Fatalf("BytesToNum() = %d, want 123", got)
	}

	var m sync.Map
	m.Store("k1", 1)
	m.Store("k2", 2)
	if got := GetSyncMapLen(&m); got != 2 {
		t.Fatalf("GetSyncMapLen() = %d, want 2", got)
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

	network, addr := BuildUdpBindAddr("203.0.113.1", nil)
	if network != "udp4" || addr == nil || addr.IP.String() != "203.0.113.1" {
		t.Fatalf("BuildUdpBindAddr(server v4) = (%q, %v)", network, addr)
	}
	network, addr = BuildUdpBindAddr("2001:db8::1", nil)
	if network != "udp6" || addr == nil || addr.IP.String() != "2001:db8::1" {
		t.Fatalf("BuildUdpBindAddr(server v6) = (%q, %v)", network, addr)
	}
	network, addr = BuildUdpBindAddr("", net.ParseIP("192.0.2.2"))
	if network != "udp4" || addr == nil || !addr.IP.Equal(net.IPv4zero) {
		t.Fatalf("BuildUdpBindAddr(client v4) = (%q, %v)", network, addr)
	}
	network, addr = BuildUdpBindAddr("", nil)
	if network != "udp" || addr == nil {
		t.Fatalf("BuildUdpBindAddr(default) = (%q, %v)", network, addr)
	}

	if !IsSameIPType("1.1.1.1:80", "2.2.2.2:90") || !IsSameIPType("[2001:db8::1]:80", "[2001:db8::2]:90") || IsSameIPType("1.1.1.1:80", "[2001:db8::2]:90") {
		t.Fatalf("IsSameIPType() returned unexpected result")
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
