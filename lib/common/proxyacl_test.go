package common

import (
	"reflect"
	"testing"
)

func TestParseProxyACLEntries(t *testing.T) {
	raw := "\n # comment\n;comment2\nEXAMPLE.com\n10.0.0.0/8\n[2001:db8::1]:443\n中文：8080\n"
	got := ParseProxyACLEntries(raw)
	want := []string{"example.com", "10.0.0.0/8", "[2001:db8::1]:443", "中文:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseProxyACLEntries() = %#v, want %#v", got, want)
	}
}

func TestParseProxyACLBuildsMatchers(t *testing.T) {
	acl := ParseProxyACL("\n127.0.0.1\n10.0.0.0/8\nEXAMPLE.com\n*.sub.example.com\n*example.com\ninvalid/path\n")

	if !acl.Enabled() {
		t.Fatalf("expected acl to be enabled")
	}
	if _, ok := acl.ExactIPs["127.0.0.1"]; !ok {
		t.Fatalf("expected exact ip matcher to contain 127.0.0.1")
	}
	if len(acl.CIDRs) != 1 {
		t.Fatalf("expected 1 CIDR matcher, got %d", len(acl.CIDRs))
	}
	if _, ok := acl.Hostnames["example.com"]; !ok {
		t.Fatalf("expected hostname matcher to contain example.com")
	}

	wantWildcards := []string{".sub.example.com", "example.com"}
	if !reflect.DeepEqual(acl.WildcardSuffixes, wantWildcards) {
		t.Fatalf("WildcardSuffixes = %#v, want %#v", acl.WildcardSuffixes, wantWildcards)
	}
}

func TestProxyACLAllows(t *testing.T) {
	acl := ParseProxyACL("\n127.0.0.1\n10.0.0.0/8\nexample.com\n*.a.com\n*a.net\n")

	tests := []struct {
		name string
		addr string
		want bool
	}{
		{name: "exact ipv4", addr: "127.0.0.1:80", want: true},
		{name: "cidr ipv4", addr: "10.11.12.13:443", want: true},
		{name: "exact hostname", addr: "EXAMPLE.COM:8443", want: true},
		{name: "subdomain wildcard only", addr: "x.a.com:443", want: true},
		{name: "root denied for subdomain-only wildcard", addr: "a.com:443", want: false},
		{name: "root allowed for include-root wildcard", addr: "a.net:443", want: true},
		{name: "subdomain allowed for include-root wildcard", addr: "b.a.net:443", want: true},
		{name: "boundary check denied", addr: "xxa.net:443", want: false},
		{name: "not in acl", addr: "deny.example.org:443", want: false},
		{name: "invalid empty", addr: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := acl.Allows(tc.addr); got != tc.want {
				t.Fatalf("Allows(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

func TestProxyACLDisabledAndNil(t *testing.T) {
	var nilACL *ProxyACL
	if nilACL.Enabled() {
		t.Fatalf("nil acl should be disabled")
	}
	if nilACL.Allows("127.0.0.1:80") {
		t.Fatalf("nil acl should not allow any addr")
	}

	empty := ParseProxyACL("\n #only comments\n")
	if empty.Enabled() {
		t.Fatalf("empty acl should be disabled")
	}
	if empty.Allows("example.com:443") {
		t.Fatalf("empty acl should not allow any addr")
	}
}

func TestNormalizeHostToken(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{name: "hostname with port", in: "Example.COM:443", want: "example.com", valid: true},
		{name: "ipv6 with brackets", in: "[2001:db8::1]:443", want: "2001:db8::1", valid: true},
		{name: "trailing dot", in: "example.com.", want: "example.com", valid: true},
		{name: "reject path", in: "example.com/path", want: "", valid: false},
		{name: "reject query", in: "example.com?x=1", want: "", valid: false},
		{name: "reject spaces", in: "example .com", want: "", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeHostToken(tc.in)
			if ok != tc.valid {
				t.Fatalf("normalizeHostToken(%q) validity = %v, want %v", tc.in, ok, tc.valid)
			}
			if got != tc.want {
				t.Fatalf("normalizeHostToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseProxyACLSkipsInvalidWildcard(t *testing.T) {
	acl := ParseProxyACL("*\n*.\n*  \n")

	if !acl.Enabled() {
		t.Fatalf("expected acl to be enabled because entries are present")
	}
	if len(acl.WildcardSuffixes) != 0 {
		t.Fatalf("expected invalid wildcard entries to be ignored, got %v", acl.WildcardSuffixes)
	}
	if acl.Allows("example.com:443") {
		t.Fatalf("acl with only invalid wildcard entries should deny all addresses")
	}
}
