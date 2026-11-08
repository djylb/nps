package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/holepunch/quicpath"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/acl"
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
		scope, err := acl.CompileScope(lp.Scope, "link scope")
		if err != nil {
			return nil, err
		}
		return &peerExit{base: base, policy: p, scope: scope, targets: &model.Target{TargetStr: p.Targets},
			resolve: net.DefaultResolver.LookupNetIP}, nil
	}
}

// peerExit checks each link against the policy before it dials: policy has
// the targets and the network, scope the rule text (§9.5).
type peerExit struct {
	base    coreapi.Exit
	policy  *proxy.SecretPolicy
	scope   *acl.Dest
	targets *model.Target
	resolve func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

var (
	errPeerDenied  = &net.OpError{Op: "dial", Err: fmt.Errorf("%w: outside the access scope", syscall.EACCES)}
	errScopeDenied = &net.OpError{Op: "dial", Err: acl.ErrScopeDenied}
)

func (e *peerExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	lk := &conn.Link{ConnType: network, Host: target}
	g, err := e.policy.Admit(lk)
	if err != nil {
		return nil, errPeerDenied
	}
	if g.Host != "" {
		return e.dialScoped(ctx, g.ConnType, g.Host)
	}
	var c net.Conn
	err = e.targets.Dial(func(addr string) (bool, error) {
		var derr error
		c, derr = e.base.Dial(ctx, g.ConnType, addr)
		return false, derr
	})
	return c, err
}

// dialScoped dials target, naming a destination the visitor chose, on the
// addresses the scope judged, so that a later DNS answer cannot change them.
func (e *peerExit) dialScoped(ctx context.Context, network, target string) (net.Conn, error) {
	if e.scope == nil {
		return e.base.Dial(ctx, network, target)
	}
	if e.scope.DeniesName(target) {
		return nil, errScopeDenied
	}
	host, p, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		ips = []netip.Addr{ip}
	} else if ips, err = e.resolve(ctx, "ip", host); err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no address", Name: host, IsNotFound: true}
	}
	if !e.scope.AllowsResolved(target, ips) {
		return nil, errScopeDenied
	}
	var derr error
	for _, ip := range ips {
		var c net.Conn
		if c, derr = e.base.Dial(ctx, network, netip.AddrPortFrom(ip.Unmap(), uint16(port)).String()); derr == nil {
			return c, nil
		}
	}
	return nil, derr
}

// AdmitUDP5 is the policy's answer to addressed UDP, before the link is up.
func (e *peerExit) AdmitUDP5() error {
	if _, err := e.policy.Admit(&conn.Link{ConnType: conn.LinkUDP5}); err != nil {
		return errPeerDenied
	}
	return nil
}

// udp5Policy is the check of each datagram's destination, nil without scope.
func (e *peerExit) udp5Policy() conn.UDP5Policy {
	if e.scope == nil {
		return nil
	}
	return func(addr string, ips []netip.Addr) bool {
		if ips == nil {
			return !e.scope.DeniesName(addr)
		}
		return e.scope.AllowsResolved(addr, ips)
	}
}

func (e *peerExit) ServeUDP5(ctx context.Context, c net.Conn) {
	if f, ok := e.base.(policyUDP5); ok {
		f.ServeUDP5Policy(ctx, c, e.udp5Policy())
		return
	}
	if e.scope != nil {
		_ = c.Close() // no way to apply the scope
		return
	}
	e.base.ServeUDP5(ctx, c)
}

// policyUDP5 is an exit that can apply a policy to each datagram.
type policyUDP5 interface {
	ServeUDP5Policy(ctx context.Context, c net.Conn, allow conn.UDP5Policy)
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
