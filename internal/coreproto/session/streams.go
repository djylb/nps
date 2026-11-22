package session

import (
	"context"
	"math"

	"github.com/djylb/lifecycle"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// rejectCode answers a stream nobody serves yet (§4.1, §11).
func rejectCode(preamble []byte, allowed func(wire.Op) bool) uint64 {
	op, _, err := wire.ParseOpenOp(preamble)
	switch {
	case err != nil:
		return wire.CodeMalformed
	case op.Known() && !allowed(op):
		return wire.CodeNotAllowed
	case op == wire.OpTCP || op == wire.OpUDP:
		// §4.6: only with the link_id of a server-exit LINK, and none
		// exists before point-to-point arrives.
		return wire.CodeNotAllowed
	}
	return wire.CodeUnsupported
}

// unboundedHandlers is the maxConcurrent of zmux's stream callbacks that do
// a stream's whole work: no bound of its own, as the streams a peer may have
// open are bounded by zmux (MaxIncomingStreams) already.
const unboundedHandlers = math.MaxInt32

// newSessionScope is the scope of a session's zmux callbacks (handleStreams):
// only a guard, whose tasks handle their own errors.
func newSessionScope() *lifecycle.Scope {
	return lifecycle.New(context.Background(), lifecycle.Isolate(func(error) {}))
}

// handleStreams serves the bidirectional streams of zc with f, each call a
// task of sc in a goroutine of its own and as many at once as the peer
// opens. Once sc drains a stream is refused with NPS_DRAIN; the owner drains
// sc after zc ended and waits for it, which zmux's Wait does not.
func handleStreams(zc *zmux.Conn, sc *lifecycle.Scope, f func(*zmux.Stream)) error {
	return zc.HandleStreams(func(st *zmux.Stream) {
		task, err := sc.Enter()
		defer task.End()
		if err != nil {
			st.CancelRead(zmux.StreamErrorCode(wire.CodeDrain))
			st.CancelWrite(zmux.StreamErrorCode(wire.CodeDrain))
			return
		}
		f(st)
	}, unboundedHandlers)
}

// rejectStreams refuses every bidirectional stream the peer opens. The
// refusal touches only the stream, so it needs no scope.
func rejectStreams(zc *zmux.Conn, allowed func(wire.Op) bool) error {
	return zc.HandleStreams(func(st *zmux.Stream) {
		c := zmux.StreamErrorCode(rejectCode(st.Preamble(), allowed))
		st.CancelRead(c)
		st.CancelWrite(c)
	}, 1)
}

// rejectUniStreams refuses every unidirectional stream the peer opens.
func rejectUniStreams(zc *zmux.Conn, allowed func(wire.Op) bool) error {
	return zc.HandleUniStreams(func(st *zmux.ReceiveStream) {
		st.CancelRead(zmux.StreamErrorCode(rejectCode(st.Preamble(), allowed)))
	}, 1)
}
