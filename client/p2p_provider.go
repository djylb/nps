package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
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
	route   string
	visitor int
}

// directGrantsRevokedFor is how long a revocation is remembered for direct
// paths of the grant still being set up.
const directGrantsRevokedFor = 10 * time.Minute

// directGrants are the direct connections a provider serves, by the grant
// nps issued the visitor: nps revoking the grant closes them.
type directGrants struct {
	mu      sync.Mutex
	conns   map[string]map[io.Closer]struct{}
	revoked map[string]time.Time
}

// add registers c of grant; false when the grant was revoked already.
func (d *directGrants) add(grant string, c io.Closer) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for g, at := range d.revoked {
		if now.Sub(at) > directGrantsRevokedFor {
			delete(d.revoked, g)
		}
	}
	if _, ok := d.revoked[grant]; ok {
		return false
	}
	if d.conns == nil {
		d.conns = make(map[string]map[io.Closer]struct{})
	}
	if d.conns[grant] == nil {
		d.conns[grant] = make(map[io.Closer]struct{})
	}
	d.conns[grant][c] = struct{}{}
	return true
}

func (d *directGrants) remove(grant string, c io.Closer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.conns[grant], c)
	if len(d.conns[grant]) == 0 {
		delete(d.conns, grant)
	}
}

// revoke closes the connections of grant and refuses new ones.
func (d *directGrants) revoke(grant string) {
	d.mu.Lock()
	conns := d.conns[grant]
	delete(d.conns, grant)
	if d.revoked == nil {
		d.revoked = make(map[string]time.Time)
	}
	d.revoked[grant] = time.Now()
	d.mu.Unlock()
	for c := range conns {
		_ = c.Close()
	}
	if len(conns) > 0 {
		p2pLog.Debug("direct paths closed", "role", "provider", "reason", "revoked", "count", len(conns))
	}
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
	return &directAccess{policy: p, targets: &model.Target{TargetStr: p.Targets}, tunnel: invite.Tunnel, route: invite.Route, visitor: invite.Visitor}, nil
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
	if !s.directs.add(invite.Grant, pc) {
		fail("revoked", errors.New("the visitor's grant was revoked"))
		return
	}
	defer s.directs.remove(invite.Grant, pc)
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
// the policy nps would apply to it on the relay; the link is acknowledged
// once admitted.
func (s *TRPClient) serveDirectLink(src net.Conn, a *directAccess) {
	lk, err := legacy.ReadLink(src)
	if err != nil || lk == nil {
		log.Debug("read link failed", "err", err)
		_ = src.Close()
		return
	}
	if lk.ConnType == linkCheck {
		if lk.Option.NeedAck {
			_ = legacy.WriteAck(src, lk.Option.Timeout)
		}
		_ = src.Close()
		return
	}
	g, err := a.policy.Admit(lk)
	if err == nil && g.LocalProxy {
		err = errDirectLocalProxy
	}
	if err != nil {
		if errors.Is(err, proxy.ErrSecretDestDenied) {
			slogx.Emit(context.Background(), p2pLog, &logevent.TunnelDenied, "src_ip", netaddr.Host(src.RemoteAddr().String()),
				"route", a.route, "tunnel", a.tunnel, "peer", a.visitor, "target", g.Host, "reason", "dest_acl", "via", "direct")
		} else {
			p2pLog.Info("link refused", "role", "provider", "tunnel", a.tunnel, "peer", a.visitor, "via", "direct",
				"network", lk.ConnType, "target", lk.Host, "err", err)
		}
		_ = src.Close()
		return
	}
	if lk.Option.NeedAck {
		if err := legacy.WriteAck(src, lk.Option.Timeout); err != nil {
			log.Debug("write ack failed", "err", err)
			_ = src.Close()
			return
		}
	}
	lk.ConnType, lk.Host, lk.LocalProxy = g.ConnType, g.Host, false
	s.serveLink(src, lk, a.targets)
}
