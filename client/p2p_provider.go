package client

import (
	"context"
	"errors"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// serveP2P answers an invitation as the provider: it joins the session on a
// new stream of the tunnel, punches, accepts the visitor's QUIC connection
// and serves its streams like the tunnel's until the connection ends or the
// client closes.
func (s *TRPClient) serveP2P(tunnel legacy.Carrier, invite legacy.P2PInvite) {
	stream, err := tunnel.OpenStream(s.ctx)
	if err != nil {
		slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "open_stream", "err", err)
		return
	}
	join := legacy.P2PJoin{SessionID: invite.SessionID, Token: invite.Token, ServerIP: s.server.ip}
	if DisableP2P {
		slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "disabled")
		join.Decline = "P2P is disabled on the provider"
		c := conn.NewConn(stream)
		if err := SendType(c, legacy.WorkP2PSession, s.uuid); err == nil {
			_ = c.SetWriteDeadline(time.Now().Add(p2pControlTimeout))
			_ = legacy.WriteP2P(c, join)
			_ = c.Close()
		}
		return
	}
	tc, _, err := openP2PControl(s.ctx, conn.NewConn(stream), legacy.WorkP2PSession, s.uuid, s.server.fingerprint, join)
	if err != nil {
		slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "join_failed", "err", err)
		return
	}
	// The provider accepts a background session whenever the server offers
	// one: the visitor relays meanwhile.
	sess, err := holepunch.Punch(s.ctx, tc, p2pOptions(s.localIP, s.mapper, true, "provider"))
	_ = tc.Close()
	if err != nil {
		if herr, ok := errors.AsType[*holepunch.Error](err); ok {
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "punch_failed", "err", err, "nat", p2pNATSummary(herr.Diagnostics))
		} else {
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "punch_failed", "err", err)
		}
		return
	}
	diag := sess.Diagnostics()
	p2pLog.Debug("punched", "role", "provider", "session", invite.SessionID, "nat", p2pNATSummary(&diag))
	pc, err := acceptP2PTransport(s.ctx, sess)
	if err != nil {
		slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "role", "provider", "session", invite.SessionID, "reason", "visitor_not_connected", "err", err)
		return
	}
	defer func() { _ = pc.Close() }()
	qc := pc.QUIC()
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PConnected, "role", "provider", "session", invite.SessionID, "via", "direct", "addr", qc.RemoteAddr().String())
	for {
		st, err := qc.AcceptStream(s.ctx)
		if err != nil {
			if cause := pc.Err(); cause != nil {
				err = cause
			}
			p2pLog.Info("direct connection closed", "role", "provider", "session", invite.SessionID, "addr", qc.RemoteAddr().String(), "err", err)
			return
		}
		go s.handleChan(conn.NewQuicStreamConn(st, qc))
	}
}
