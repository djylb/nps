package client

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/p2pproto"
)

func TestP2PBackoffActions(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want holepunch.Action
	}{
		{"old server", fmt.Errorf("%w: eof", errP2PServerIncompatible), holepunch.ActionGiveUp},
		{"task refused", &p2pproto.RefusedError{Code: p2pproto.CodeRefused}, holepunch.ActionGiveUp},
		{"old provider", &p2pproto.RefusedError{Code: p2pproto.CodeUnsupported}, holepunch.ActionGiveUp},
		{"provider offline", &p2pproto.RefusedError{Code: p2pproto.CodeOffline}, holepunch.ActionRetry},
		{"server busy", &p2pproto.RefusedError{Code: p2pproto.CodeBusy}, holepunch.ActionRetry},
		{"bridge i/o", errors.New("connection reset"), holepunch.ActionRetry},
		{"path lost", errP2PPathLost, holepunch.ActionRetry},
		{"hard nat", &holepunch.Error{Reason: protocol.ReasonHardSymmetricRandom}, holepunch.ActionBackground},
		{"relay only", &holepunch.Error{Reason: protocol.ReasonCandidateTimeout, Advice: protocol.AdviceRelayOnly}, holepunch.ActionRelayOnly},
	}
	for _, tc := range cases {
		var b p2pBackoff
		if got := b.failed(tc.err).Action; got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestP2PBackoffCountsSameAction(t *testing.T) {
	var b p2pBackoff
	offline := &p2pproto.RefusedError{Code: p2pproto.CodeOffline}
	for range 5 {
		b.failed(offline)
	}
	if a := b.failed(offline); a.Delay < 8*time.Second {
		t.Fatalf("sixth failure waits %s, want at least 8s", a.Delay)
	}
	// Another action starts its own count.
	b.failed(&holepunch.Error{Reason: protocol.ReasonHardSymmetricRandom})
	if a := b.failed(offline); a.Delay > time.Second {
		t.Fatalf("first transient failure after another kind waits %s", a.Delay)
	}
}
