package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/holepunch/quicpath"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/quic-go/quic-go"
)

// coreRequest is a listener's link as the new protocol asks for it.
func coreRequest(link *conn.Link) coreapi.LinkRequest {
	r := coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: link.Host, WantReply: link.Option.NeedAck}
	switch link.ConnType {
	case conn.LinkUDP:
		r.Network = coreapi.NetworkUDP
	case conn.LinkUDP5:
		r.Network, r.Target = coreapi.NetworkUDP, ""
		return r
	}
	r.ProviderTarget = r.Target == ""
	return r
}

// NewCorePeerExit makes the exit of a link this client provides: base, under
// the link's policy.
func NewCorePeerExit(base coreapi.Exit) func(coreapi.LinkPolicy) (coreapi.Exit, error) {
	return func(lp coreapi.LinkPolicy) (coreapi.Exit, error) {
		p := &proxy.SecretPolicy{Targets: strings.Join(lp.Targets, "\n"), TargetType: lp.TargetType, NameDest: lp.NameDest}
		if p.TargetType == "" {
			p.TargetType = model.TargetAll
		}
		p.DestAclMode, p.DestAclRules = aclOfScope(lp.Scope)
		if err := p.Compile(); err != nil {
			return nil, err
		}
		return &peerExit{base: base, policy: p, targets: &model.Target{TargetStr: p.Targets}}, nil
	}
}

// aclOfScope reads rule text (§9.5) as a task's destination list: all deny
// lines are a denylist, otherwise the lines are an allowlist.
func aclOfScope(scope string) (int, string) {
	if scope == "" {
		return model.AclOff, ""
	}
	var rules []string
	deny := true
	for line := range strings.Lines(scope) {
		r := strings.TrimSpace(line)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(r, "deny "); ok {
			r = strings.TrimSpace(rest)
		} else {
			deny = false
			r = strings.TrimSpace(strings.TrimPrefix(r, "allow "))
		}
		rules = append(rules, r)
	}
	if deny {
		return model.AclBlacklist, strings.Join(rules, "\n")
	}
	return model.AclWhitelist, strings.Join(rules, "\n")
}

// peerExit checks each link against the policy before it dials.
type peerExit struct {
	base    coreapi.Exit
	policy  *proxy.SecretPolicy
	targets *model.Target
}

var errPeerDenied = &net.OpError{Op: "dial", Err: fmt.Errorf("%w: outside the access scope", syscall.EACCES)}

func (e *peerExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	lk := &conn.Link{ConnType: network, Host: target}
	g, err := e.policy.Admit(lk)
	if err != nil {
		return nil, errPeerDenied
	}
	if g.Host != "" {
		return e.base.Dial(ctx, g.ConnType, g.Host)
	}
	var c net.Conn
	err = e.targets.Dial(func(addr string) (bool, error) {
		var derr error
		c, derr = e.base.Dial(ctx, g.ConnType, addr)
		return false, derr
	})
	return c, err
}

func (e *peerExit) ServeUDP5(ctx context.Context, c net.Conn) {
	if _, err := e.policy.Admit(&conn.Link{ConnType: conn.LinkUDP5}); err != nil {
		_ = c.Close()
		return
	}
	e.base.ServeUDP5(ctx, c)
}

// corePuncher makes direct paths with holepunch and QUIC.
type corePuncher struct {
	localIP string
	mapper  *portmapper.Mapper
	quic    func(*quic.Config) *quic.Config
}

// NewCorePuncher returns the puncher of localIP; quicConfig completes the QUIC
// configuration of a direct path.
func NewCorePuncher(localIP string, quicConfig func(*quic.Config) *quic.Config) coreapi.Puncher {
	return &corePuncher{localIP: localIP, quic: quicConfig, mapper: portmapper.New(portmapper.Config{Network: holepunch.SystemNetwork()})}
}

func (p *corePuncher) Punch(ctx context.Context, ctrl net.Conn, background, accept bool) (*coreapi.DirectPath, error) {
	role := "visitor"
	if accept {
		role = "provider"
	}
	opts := p2pOptions(p.localIP, p.mapper, background || accept, role)
	opts.ControlSecurity = protocol.ControlSecurityDeclared
	sess, err := holepunch.Punch(ctx, ctrl, opts)
	if err != nil {
		return nil, err
	}
	pin, err := p2pPeerPin(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	qcfg := p.quic(p2pQUICConfig())
	var c *quicpath.Conn
	if accept {
		c, err = quicpath.Accept(ctx, sess, withH3(p2pServerTLS(pin)), qcfg)
	} else {
		c, err = quicpath.Dial(ctx, sess, withH3(p2pClientTLS(pin)), qcfg)
	}
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	return directPathOf(c.QUIC(), func() { _ = c.Close() }), nil
}

func directPathOf(qc *quic.Conn, closeFn func()) *coreapi.DirectPath {
	return &coreapi.DirectPath{QUIC: qc, Close: closeFn}
}

func withH3(t *tls.Config) *tls.Config {
	t.NextProtos = []string{"h3"}
	return t
}
