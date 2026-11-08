package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/slogx"
)

// linkCheck is the type of a link the visitor sends on the direct path only
// to have it acknowledged: a liveness check of the path.
const linkCheck = "check"

var errDirectLocalProxy = errors.New("the server reaches this destination itself: relay only")

// directAccess is what one direct path of a secret task lets its visitor
// reach: the policy nps sent with the invitation, and the task's targets.
type directAccess struct {
	policy  *proxy.SecretPolicy
	targets *model.Target
	tunnel  int
	visitor int
}

func newDirectAccess(invite legacy.P2PInvite) (*directAccess, error) {
	p := new(proxy.SecretPolicy)
	if len(invite.Policy) == 0 {
		return nil, errors.New("invitation without a policy")
	}
	if err := json.Unmarshal(invite.Policy, p); err != nil {
		return nil, err
	}
	if err := p.Compile(); err != nil {
		return nil, err
	}
	return &directAccess{policy: p, targets: &model.Target{TargetStr: p.Targets}, tunnel: invite.Tunnel, visitor: invite.Visitor}, nil
}

// serveSecretDirect answers an invitation as the provider: it joins the
// session on a new stream of the tunnel, punches, accepts the visitor's QUIC
// connection and serves its streams by the invitation's policy until the
// connection ends or the client closes.
func (s *TRPClient) serveSecretDirect(tunnel legacy.Carrier, invite legacy.P2PInvite) {
	fail := func(reason string, err error, args ...any) {
		args = append([]any{"role", "provider", "session", invite.SessionID, "tunnel", invite.Tunnel, "peer", invite.Visitor, "reason", reason, "err", err}, args...)
		slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, args...)
	}
	access, policyErr := newDirectAccess(invite)
	stream, err := tunnel.OpenStream(s.ctx)
	if err != nil {
		fail("open_stream", err)
		return
	}
	join := legacy.P2PJoin{SessionID: invite.SessionID, Token: invite.Token, ServerIP: s.server.ip}
	if policyErr != nil {
		fail("policy", policyErr)
		join.Decline = "the provider cannot apply the task's policy: " + policyErr.Error()
		c := conn.NewConn(stream)
		if err := SendType(c, legacy.WorkP2PSession, s.uuid); err == nil {
			_ = c.SetWriteDeadline(time.Now().Add(p2pControlTimeout))
			_ = legacy.WriteP2P(c, join)
		}
		_ = c.Close()
		return
	}
	tc, _, err := openP2PControl(s.ctx, conn.NewConn(stream), legacy.WorkP2PSession, s.uuid, s.server.fingerprint, join)
	if err != nil {
		fail("join_failed", err)
		return
	}
	// The provider accepts a background session whenever the server offers
	// one: the visitor relays meanwhile.
	sess, err := holepunch.Punch(s.ctx, tc, p2pOptions(s.localIP, s.mapper, true, "provider"))
	_ = tc.Close()
	if err != nil {
		if herr, ok := errors.AsType[*holepunch.Error](err); ok {
			fail("punch_failed", err, "nat", p2pNATSummary(herr.Diagnostics))
		} else {
			fail("punch_failed", err)
		}
		return
	}
	diag := sess.Diagnostics()
	p2pLog.Debug("punched", "role", "provider", "session", invite.SessionID, "nat", p2pNATSummary(&diag))
	pc, err := acceptP2PTransport(s.ctx, sess)
	if err != nil {
		fail("visitor_not_connected", err)
		return
	}
	defer func() { _ = pc.Close() }()
	qc := pc.QUIC()
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PConnected, "role", "provider", "session", invite.SessionID,
		"tunnel", invite.Tunnel, "peer", invite.Visitor, "via", "direct", "addr", qc.RemoteAddr().String())
	for {
		st, err := qc.AcceptStream(s.ctx)
		if err != nil {
			if cause := pc.Err(); cause != nil {
				err = cause
			}
			p2pLog.Info("direct connection closed", "role", "provider", "session", invite.SessionID, "tunnel", invite.Tunnel,
				"peer", invite.Visitor, "addr", qc.RemoteAddr().String(), "err", err)
			return
		}
		go s.serveDirectLink(conn.NewQuicStreamConn(st, qc), access)
	}
}

// serveDirectLink serves a link the visitor sent over the direct path, by
// the policy nps would apply to it on the relay.
func (s *TRPClient) serveDirectLink(src net.Conn, a *directAccess) {
	lk := readLink(src)
	if lk == nil {
		return
	}
	if lk.ConnType == linkCheck {
		_ = src.Close()
		return
	}
	g, err := a.policy.Admit(lk)
	if err == nil && g.LocalProxy {
		err = errDirectLocalProxy
	}
	if err != nil {
		p2pLog.Info("link refused", "role", "provider", "tunnel", a.tunnel, "peer", a.visitor, "via", "direct",
			"network", lk.ConnType, "target", lk.Host, "err", err)
		_ = src.Close()
		return
	}
	lk.ConnType, lk.Host, lk.LocalProxy = g.ConnType, g.Host, false
	s.serveLink(src, lk, a.targets)
}
