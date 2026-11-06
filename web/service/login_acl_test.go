package service

import (
	"testing"

	"github.com/djylb/nps/lib/acl"
)

func TestLoginACLWithUnusableRules(t *testing.T) {
	policy := func(mode int, rules string) func(string) bool {
		src := compileLoginACL(mode, rules)
		return func(ip string) bool { return ip == "" || acl.AllowsString(ip, src, nil, nil) }
	}
	// Whitelist rules that all fail leave nobody allowed, not everybody.
	for _, rules := range []string{"geoip:zz", "ext:nope.dat:x", "999.1.1.1/40", "geoip:zz,10.0.0.1"} {
		if policy(1, rules)("8.8.8.8") {
			t.Errorf("whitelist %q allowed an unlisted address", rules)
		}
	}
	if !policy(1, "geoip:zz,10.0.0.1")("10.0.0.1") {
		t.Error("whitelist lost its usable rule")
	}
	if policy(1, "bad rule!!")("8.8.8.8") {
		t.Error("bad whitelist allowed an address")
	}
	if !policy(2, "10.0.0.1")("8.8.8.8") || policy(2, "10.0.0.1")("10.0.0.1") {
		t.Error("blacklist")
	}
	if !policy(1, "  ")("8.8.8.8") || !policy(0, "bad rule!!")("8.8.8.8") {
		t.Error("no list restricts nothing")
	}
}
