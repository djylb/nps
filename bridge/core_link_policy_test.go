package bridge

import (
	"slices"
	"testing"

	"github.com/djylb/nps/lib/proxy"
)

// The targets a provider receives fill in the local machine for a bare port,
// as older versions wrote them ("8080") and as ":8080", and keep every other
// form, IPv6 included.
func TestLinkPolicyTargets(t *testing.T) {
	p := &proxy.SecretPolicy{Targets: "8080\n:9090\n 10.0.0.1:22 \n\n[::1]:53\n[2001:db8::1]:443\nexample.com:443\n"}
	got := linkPolicyOf(p).Targets
	want := []string{"127.0.0.1:8080", "127.0.0.1:9090", "10.0.0.1:22", "[::1]:53", "[2001:db8::1]:443", "example.com:443"}
	if !slices.Equal(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}
