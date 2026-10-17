package client

import (
	"errors"
	"testing"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/p2pproto"
)

func TestClassifyP2PError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want p2pBackoffClass
	}{
		{"control link closed", &holepunch.Error{Reason: protocol.ReasonCanceled}, p2pBackoffTransient},
		{"peer offline", &holepunch.Error{Reason: protocol.ReasonPeerOffline}, p2pBackoffTransient},
		{"server busy", &holepunch.Error{Reason: protocol.ReasonServerBusy}, p2pBackoffTransient},
		{"hard nat", &holepunch.Error{Reason: protocol.ReasonHardSymmetricRandom}, p2pBackoffNATHard},
		{"udp blocked", &holepunch.Error{Reason: protocol.ReasonUDPBlocked}, p2pBackoffFatal},
		{"unknown reason", &holepunch.Error{Reason: "future_reason"}, p2pBackoffTransient},
		{"relay only", &holepunch.Error{Reason: protocol.ReasonCandidateTimeout, Advice: protocol.AdviceRelayOnly}, p2pBackoffRelayOnly},
		{"task refused", &p2pproto.RefusedError{Code: p2pproto.CodeRefused}, p2pBackoffFatal},
		{"old provider", &p2pproto.RefusedError{Code: p2pproto.CodeUnsupported}, p2pBackoffFatal},
		{"provider offline", &p2pproto.RefusedError{Code: p2pproto.CodeOffline}, p2pBackoffTransient},
		{"bridge i/o", errors.New("connection reset"), p2pBackoffTransient},
	}
	for _, tc := range cases {
		if got, _, _ := classifyP2PError(tc.err); got != tc.want {
			t.Errorf("%s: class %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestP2PBackoffSchedule(t *testing.T) {
	var b p2pBackoff
	hard := &holepunch.Error{Reason: protocol.ReasonHardSymmetricRandom}
	for i, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		if got, bg := b.failed(hard, true, false); got != want || bg {
			t.Fatalf("nat_hard failure %d: wait %s background %v, want %s", i+1, got, bg, want)
		}
	}
	for range 10 {
		b.failed(hard, false, false)
	}
	if got, _ := b.failed(hard, false, false); got != 10*time.Minute {
		t.Fatalf("nat_hard wait not capped: %s", got)
	}

	// A new class starts its own schedule.
	if got, _ := b.failed(&holepunch.Error{Reason: protocol.ReasonUDPBlocked}, false, false); got != 10*time.Minute {
		t.Fatalf("fatal wait %s", got)
	}
	got, _ := b.failed(&holepunch.Error{Reason: protocol.ReasonPeerOffline}, false, false)
	if got < 5*time.Second || got >= 10*time.Second {
		t.Fatalf("transient wait %s, want [5s, 10s)", got)
	}

	// retry_after wins when it is longer.
	b.reset()
	if got, _ := b.failed(&holepunch.Error{Reason: protocol.ReasonServerBusy, RetryAfter: time.Hour}, false, false); got != time.Hour {
		t.Fatalf("retry_after ignored: %s", got)
	}
}

func TestP2PBackoffBackgroundWhileRelaying(t *testing.T) {
	viable := &holepunch.Error{Reason: protocol.ReasonCandidateTimeout, Advice: protocol.AdviceBackgroundViable}

	var b p2pBackoff
	if got, bg := b.failed(viable, true, false); got != 0 || !bg {
		t.Fatalf("first background_viable while relaying: wait %s background %v, want 0 true", got, bg)
	}
	if got, bg := b.failed(viable, true, true); got == 0 || !bg {
		t.Fatalf("failed background attempt: wait %s background %v, want a wait and background", got, bg)
	}

	b.reset()
	if got, bg := b.failed(viable, false, false); got == 0 || bg {
		t.Fatalf("background_viable without relay: wait %s background %v, want interactive after a wait", got, bg)
	}
}
