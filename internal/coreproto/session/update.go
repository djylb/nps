package session

import (
	"context"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

// updateReplyTimeout bounds npc's answer to an update request.
const updateReplyTimeout = 10 * time.Second

// CanUpdate implements coreapi.Session.
func (s *serverSession) CanUpdate() bool { return s.t.hello.HasCap(wire.CapUpdate) }

// ShapingStats implements coreapi.Session.
func (s *serverSession) ShapingStats() coreapi.ShapingStats {
	st := s.zc.ShapingStats()
	return coreapi.ShapingStats{Wire: st.WireSpent, Stall: st.StallSpent, Injected: st.Injected,
		Clamped: st.Clamped, InjectDropped: st.InjectDropped, Faults: st.Faults}
}

// RequestUpdate implements coreapi.Session (§6).
func (s *serverSession) RequestUpdate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, updateReplyTimeout)
	defer cancel()
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	lc := s.conn(st)
	if err := st.SetPreamble(wire.AppendOpenInfo(nil, wire.OpUpdate)); err != nil {
		lc.abort(wire.CodeInternal)
		return err
	}
	if err := st.Flush(); err != nil {
		lc.abort(wire.CodeInternal)
		return err
	}
	defer func() { _ = lc.Close() }()
	return readLinkReply(ctx, lc)
}

// serveUpdate answers an update request by the local policy (§6).
func (s *clientSession) serveUpdate(lc *linkConn) {
	defer func() { _ = lc.Close() }()
	code := wire.CodeNotAllowed
	if s.d.cfg.Update != nil && s.d.cfg.Update() {
		code = wire.CodeOK
	}
	_ = lc.SetWriteDeadline(time.Now().Add(updateReplyTimeout))
	if _, err := lc.Write(wire.AppendReply(nil, code, "", nil)); err == nil && code != wire.CodeOK {
		lc.SetReliableBoundary()
	}
}
