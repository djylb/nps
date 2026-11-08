package session

import (
	"context"

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

// rejectStreams refuses every bidirectional stream the peer opens.
func rejectStreams(ctx context.Context, zc *zmux.Conn, allowed func(wire.Op) bool) {
	for {
		st, err := zc.AcceptStream(ctx)
		if err != nil {
			return
		}
		c := zmux.StreamErrorCode(rejectCode(st.Preamble(), allowed))
		st.CancelRead(c)
		st.CancelWrite(c)
	}
}

// rejectUniStreams refuses every unidirectional stream the peer opens.
func rejectUniStreams(ctx context.Context, zc *zmux.Conn, allowed func(wire.Op) bool) {
	for {
		st, err := zc.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		st.CancelRead(zmux.StreamErrorCode(rejectCode(st.Preamble(), allowed)))
	}
}
