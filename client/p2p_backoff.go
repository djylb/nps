package client

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/p2pproto"
)

// p2pBackoffClass groups P2P failures by what retrying them achieves.
type p2pBackoffClass uint8

const (
	// p2pBackoffTransient: the peer, the server or the control link failed
	// for now (holepunch ClassTransient, a canceled session whose control
	// link went away, a busy server, an offline provider, I/O errors); retry
	// soon.
	p2pBackoffTransient p2pBackoffClass = iota
	// p2pBackoffNATHard: the NAT pair did not connect this time (holepunch
	// ClassNATHard); the server's strategy memory tries another plan later.
	p2pBackoffNATHard
	// p2pBackoffRelayOnly: the server expects no mode to connect this pair
	// (advice relay_only); stay on the relay for long.
	p2pBackoffRelayOnly
	// p2pBackoffFatal: configuration, version or local network problems
	// (holepunch ClassFatal, a refused task); retrying soon ends the same
	// way.
	p2pBackoffFatal
)

// p2pSchedule is a backoff: base doubling per consecutive failure of the
// same class up to max, plus a uniform jitter in [0, base) when set.
type p2pSchedule struct {
	base, max time.Duration
	jitter    bool
}

var p2pSchedules = [...]p2pSchedule{
	p2pBackoffTransient: {base: 5 * time.Second, max: time.Minute, jitter: true},
	p2pBackoffNATHard:   {base: 30 * time.Second, max: 10 * time.Minute},
	p2pBackoffRelayOnly: {base: 2 * time.Minute, max: 30 * time.Minute},
	p2pBackoffFatal:     {base: 10 * time.Minute, max: 10 * time.Minute},
}

// classifyP2PError returns the backoff class of a failed punch or transport
// setup, the server's retry_after and its advice.
func classifyP2PError(err error) (p2pBackoffClass, time.Duration, protocol.Advice) {
	var herr *holepunch.Error
	var refused *p2pproto.RefusedError
	switch {
	case errors.As(err, &herr):
		class := p2pBackoffTransient
		switch herr.Reason.Class() {
		case protocol.ClassFatal:
			class = p2pBackoffFatal
		case protocol.ClassNATHard:
			class = p2pBackoffNATHard
		}
		if herr.Advice == protocol.AdviceRelayOnly {
			class = p2pBackoffRelayOnly
		}
		return class, herr.RetryAfter, herr.Advice
	case errors.Is(err, errP2PServerIncompatible):
		return p2pBackoffFatal, 0, ""
	case errors.As(err, &refused):
		switch refused.Code {
		case p2pproto.CodeOffline, p2pproto.CodeBusy:
			return p2pBackoffTransient, 0, ""
		}
		return p2pBackoffFatal, 0, ""
	}
	return p2pBackoffTransient, 0, ""
}

// p2pBackoff decides when and how a visitor punches again.
type p2pBackoff struct {
	class p2pBackoffClass
	fails int // consecutive failures of class
}

// failed records a failed attempt and returns the wait before the next one
// and whether the next one runs in background mode. relay says whether
// connections are relayed meanwhile; background whether the failed attempt
// ran in background mode. A failure advising background_viable while relaying
// is retried in background mode right away the first time. The caller
// does not record attempts its own closing canceled.
func (b *p2pBackoff) failed(err error, relay, background bool) (time.Duration, bool) {
	class, retryAfter, advice := classifyP2PError(err)
	if class != b.class {
		b.class, b.fails = class, 0
	}
	b.fails++
	nextBackground := relay && advice == protocol.AdviceBackgroundViable
	if nextBackground && !background {
		return 0, true
	}
	s := p2pSchedules[class]
	wait := s.base
	for i := 1; i < b.fails && wait < s.max; i++ {
		wait *= 2
	}
	wait = min(wait, s.max)
	if s.jitter {
		wait += rand.N(s.base)
	}
	return max(wait, retryAfter), nextBackground
}

// reset forgets the failures after a success.
func (b *p2pBackoff) reset() { *b = p2pBackoff{} }
