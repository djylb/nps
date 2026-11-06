package client

import (
	"errors"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/p2pproto"
)

// p2pAttemptError expresses the failures nps adds to the hole punching
// library's in the library's reasons, so that holepunch.NextAttempt decides
// for both: a server without this P2P protocol and a refused task are fatal,
// an offline provider and a busy server transient.
func p2pAttemptError(err error) error {
	reason := protocol.Reason("")
	var refused *p2pproto.RefusedError
	switch {
	case errors.Is(err, errP2PServerIncompatible):
		reason = protocol.ReasonControlVersionMismatch
	case errors.As(err, &refused):
		switch refused.Code {
		case p2pproto.CodeOffline:
			reason = protocol.ReasonPeerOffline
		case p2pproto.CodeBusy:
			reason = protocol.ReasonServerBusy
		case p2pproto.CodeUnsupported:
			reason = protocol.ReasonControlVersionMismatch
		default:
			reason = protocol.ReasonInvalidArgument
		}
	default:
		return err
	}
	return &holepunch.Error{Reason: reason, Err: err}
}

// p2pBackoff counts a visitor's consecutive failures of the same kind
// (holepunch.Action) for holepunch.NextAttempt.
type p2pBackoff struct {
	action holepunch.Action
	fails  int
}

// failed records a failed attempt and returns what to do next. The caller
// does not record attempts its own closing canceled.
func (b *p2pBackoff) failed(err error) holepunch.Attempt {
	err = p2pAttemptError(err)
	if a := holepunch.NextAttempt(err, 1).Action; a != b.action || b.fails == 0 {
		b.action, b.fails = a, 0
	}
	b.fails++
	return holepunch.NextAttempt(err, b.fails)
}

// reset forgets the failures after a success.
func (b *p2pBackoff) reset() { *b = p2pBackoff{} }
