package client

import (
	"errors"
	"net"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/quic-go/quic-go"
)

// serveP2P answers an invitation as the provider: it joins the session on a
// new stream of the tunnel, punches, accepts the visitor's QUIC connection
// and serves its streams like the tunnel's until the connection ends or the
// client closes.
func (s *TRPClient) serveP2P(tunnel any, invite p2pproto.Invite) {
	stream, err := s.openTunnelStream(tunnel)
	if err != nil {
		logs.Warn("P2P session %s: cannot open a stream to the server: %v", invite.SessionID, err)
		return
	}
	join := p2pproto.Join{SessionID: invite.SessionID, Token: invite.Token, ServerIP: s.server.ip}
	if DisableP2P {
		logs.Info("P2P session %s declined: P2P is disabled on this client", invite.SessionID)
		join.Decline = "P2P is disabled on the provider"
		c := conn.NewConn(stream)
		if err := SendType(c, common.WorkP2PSession, s.uuid); err == nil {
			_ = c.SetWriteDeadline(time.Now().Add(p2pControlTimeout))
			_ = p2pproto.Write(c, join)
			_ = c.Close()
		}
		return
	}
	tc, _, err := openP2PControl(s.ctx, conn.NewConn(stream), common.WorkP2PSession, s.uuid, s.server.fingerprint, join)
	if err != nil {
		logs.Warn("P2P session %s: join failed: %v", invite.SessionID, err)
		return
	}
	// The provider accepts a background session whenever the server offers
	// one: the visitor relays meanwhile.
	sess, err := holepunch.Punch(s.ctx, tc, p2pOptions(s.localIP, s.mapper, true))
	_ = tc.Close()
	if err != nil {
		var herr *holepunch.Error
		if errors.As(err, &herr) {
			logs.Warn("P2P session %s: no direct path: %v; local NAT %s", invite.SessionID, err, p2pNATSummary(herr.Diagnostics))
		} else {
			logs.Warn("P2P session %s: no direct path: %v", invite.SessionID, err)
		}
		return
	}
	diag := sess.Diagnostics()
	logs.Info("P2P session %s: punched %s; local NAT %s", invite.SessionID, p2pResultSummary(sess), p2pNATSummary(&diag))
	t, err := acceptP2PTransport(s.ctx, sess)
	if err != nil {
		logs.Warn("P2P session %s: visitor did not connect over the direct path: %v", invite.SessionID, err)
		return
	}
	defer t.Close(p2pCodeClosed, "closed")
	logs.Info("P2P session %s: direct QUIC connection from %v established", invite.SessionID, t.qc.RemoteAddr())
	for {
		st, err := t.qc.AcceptStream(s.ctx)
		if err != nil {
			logs.Info("P2P session %s: direct connection from %v closed: %v", invite.SessionID, t.qc.RemoteAddr(), err)
			return
		}
		go s.handleChan(conn.NewQuicStreamConn(st, t.qc))
	}
}

// openTunnelStream opens a stream to the bridge on the client's tunnel.
func (s *TRPClient) openTunnelStream(tunnel any) (net.Conn, error) {
	switch t := tunnel.(type) {
	case *mux.Mux:
		return t.NewConn()
	case *quic.Conn:
		st, err := t.OpenStreamSync(s.ctx)
		if err != nil {
			return nil, err
		}
		return conn.NewQuicStreamConn(st, t), nil
	}
	return nil, errors.New("no tunnel to the server")
}
