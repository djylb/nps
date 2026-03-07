package common

import (
	"bytes"
	"encoding/binary"
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
