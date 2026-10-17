package common

import (
	"encoding/base64"
	"net/http/httptest"
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
		wantProxy     bool
		wantReverse   bool
	}{
		{"both, proxy header valid", basic("origin:secret"), "basic " + base64.StdEncoding.EncodeToString([]byte("user:pass")), true, false},
		{"both, authorization valid", basic("user:pass"), basic("other:secret"), false, true},
		{"only authorization", basic("user:pass"), "", true, true},
		{"only proxy header", "", basic("user:pass"), true, true},
		{"non-basic scheme", "", "Bearer " + base64.StdEncoding.EncodeToString([]byte("user:pass")), false, false},
		{"none", "", "", false, false},
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
			if got := CheckProxyAuth(r, "user", "pass", nil, nil); got != tt.wantProxy {
				t.Fatalf("CheckProxyAuth() = %v, want %v", got, tt.wantProxy)
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
	if CheckProxyAuth(r, "user", "", nil, nil) {
		t.Fatal("CheckProxyAuth() let an unauthenticated request through")
	}
	r.Header.Set("Proxy-Authorization", basic("user:"))
	if !CheckProxyAuth(r, "user", "", nil, nil) {
		t.Fatal("CheckProxyAuth() rejected the configured username with an empty password")
	}
}
