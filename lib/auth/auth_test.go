package auth

import (
	"encoding/base64"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func basic(cred string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
}

func TestCredentialHeaderPrecedence(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		proxyAuth     string
		wantReverse   bool
	}{
		{"both, proxy header valid", basic("origin:secret"), "basic " + base64.StdEncoding.EncodeToString([]byte("user:pass")), false},
		{"both, authorization valid", basic("user:pass"), basic("other:secret"), true},
		{"only authorization", basic("user:pass"), "", true},
		{"only proxy header", "", basic("user:pass"), true},
		{"non-basic scheme", "", "Bearer " + base64.StdEncoding.EncodeToString([]byte("user:pass")), false},
		{"none", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.com/", nil)
			if tt.authorization != "" {
				r.Header.Set("Authorization", tt.authorization)
			}
			if tt.proxyAuth != "" {
				r.Header.Set("Proxy-Authorization", tt.proxyAuth)
			}
			if got := CheckAuth(r, "user", "pass", nil, nil); got != tt.wantReverse {
				t.Fatalf("CheckAuth() = %v, want %v", got, tt.wantReverse)
			}
		})
	}
}

func TestUsernameOnlyRequiresAuth(t *testing.T) {
	if !NeedsAuth("user", "", nil, nil) {
		t.Fatal("NeedsAuth() = false with a username configured")
	}
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	if CheckAuth(r, "user", "", nil, nil) {
		t.Fatal("CheckAuth() let an unauthenticated request through")
	}
	r.Header.Set("Authorization", basic("user:"))
	if !CheckAuth(r, "user", "", nil, nil) {
		t.Fatal("CheckAuth() rejected the configured username with an empty password")
	}
}

func TestPowHelpers(t *testing.T) {
	if ValidatePoW(0, "abc") {
		t.Fatal("ValidatePoW(bits<1) = true, want false")
	}
	found := false
	for i := range 64 {
		if ValidatePoW(1, "seed", string(rune('A'+i))) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ValidatePoW(1, seed+suffix) never succeeded in search window")
	}
	if ValidatePoW(256, "abc") {
		t.Fatal("ValidatePoW(256, abc) = true, want false")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	for _, tc := range []struct {
		list, addr string
		want       bool
	}{
		{"*", "203.0.113.1", true},
		{"10.0.*.*,192.168.1.0/24,2001:db8::1", "10.0.5.9", true},
		{"10.0.*.*,192.168.1.0/24,2001:db8::1", "2001:db8::1", true},
		{"10.0.*.*,192.168.1.0/24,2001:db8::1", "192.168.2.1", false},
		{"bad, 127.0.0.1", "127.0.0.1", true},
		{"", "127.0.0.1", false},
	} {
		if got := ParseTrustedProxies(tc.list).Contains(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("ParseTrustedProxies(%q).Contains(%s) = %v, want %v", tc.list, tc.addr, got, tc.want)
		}
	}
}
