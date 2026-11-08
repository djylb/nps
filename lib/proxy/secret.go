package proxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

// SecretPolicy is what a visitor of a secret task may reach, its access
// scope. nps resolves it once per grant (an attached visitor) and applies it
// to the links it relays (SecretServer); the invitation of a direct path
// hands it to the provider npc, which applies it to every link the visitor
// sends over that path: both paths follow the same rules.
//
// The scope is the task's destination list. Without one the visitor reaches
// the task's targets only: a link naming no destination goes to one of
// them, one naming a destination that is not a target is refused. With one
// the visitor may name a destination and its network within the list
// (mixed and transparent visitors always do).
type SecretPolicy struct {
	// Targets are the task's target addresses, one per line, used when the
	// visitor names none, and the only ones it may name without NameDest.
	Targets string `json:"targets,omitempty"`
	// TargetType is the task's network for its targets: tcp, udp or all.
	TargetType string `json:"target_type,omitempty"`
	// NameDest lets the visitor name destinations, within the destination
	// list when the task has one.
	NameDest bool `json:"name_dest,omitempty"`
	// AllowLocal lets a visitor naming destinations choose whether nps
	// reaches them itself (allow_secret_local and allow_local_proxy).
	AllowLocal bool `json:"allow_local,omitempty"`
	// LocalProxy is whether nps reaches the target itself when the visitor
	// does not choose: then no direct path can serve the link.
	LocalProxy bool `json:"local_proxy,omitempty"`
	// DestAclMode and DestAclRules are the task's destination list, applied
	// to destinations the visitor names; mode off with NameDest is no limit.
	DestAclMode  int    `json:"dest_acl_mode,omitempty"`
	DestAclRules string `json:"dest_acl_rules,omitempty"`

	allows func(addr string) bool
}

// SecretRoute is what an admitted link reaches: Host empty means one of
// the policy's targets.
type SecretRoute struct {
	ConnType   string
	Host       string
	LocalProxy bool
}

var (
	// ErrSecretLinkRefused is a link of a network the policy does not know.
	ErrSecretLinkRefused = errors.New("secret link refused by the task's policy")
	// ErrSecretDestDenied is a destination or network outside the scope.
	ErrSecretDestDenied = errors.New("destination outside the secret task's access scope")
)

// NewSecretPolicy is the policy of task t reaching target, with the
// server's allow_local_proxy and allow_secret_local. anyDest is the legacy
// allow_secret_link: a task without a destination list lets the visitor
// name any destination.
func NewSecretPolicy(t *model.Tunnel, target *model.Target, allowLocalProxy, allowLocal, anyDest bool) *SecretPolicy {
	scoped := t.DestAclMode == model.AclWhitelist || t.DestAclMode == model.AclBlacklist
	p := &SecretPolicy{
		TargetType: t.TargetType,
		NameDest:   scoped || anyDest,
	}
	p.AllowLocal = p.NameDest && allowLocal && allowLocalProxy
	if scoped {
		p.DestAclMode, p.DestAclRules, p.allows = t.DestAclMode, t.DestAclRules, t.AllowsDestination
	}
	if target != nil {
		p.Targets = target.TargetStr
		p.LocalProxy = allowLocalProxy && target.LocalProxy
	}
	if t.Client != nil && t.Client.Id < 0 {
		// The server's own client: nps always reaches the target itself.
		p.LocalProxy, p.AllowLocal = true, false
	}
	return p
}

// Compile prepares a policy received on the wire for Admit. A destination
// list with rules this side cannot apply (geodata it lacks) is an error:
// the direct path must not allow what nps would refuse.
func (p *SecretPolicy) Compile() error {
	d, err := acl.CompileDest(p.DestAclMode, p.DestAclRules, "secret policy")
	if err != nil {
		return err
	}
	if d != nil {
		p.allows = d.Allows
	}
	return nil
}

// Direct reports whether a direct path can serve some link: not when nps
// must reach the target itself.
func (p *SecretPolicy) Direct() bool {
	return !p.LocalProxy || p.AllowLocal
}

// Admit decides what lk reaches; lk nil is a visitor that sent no link
// information and gets the task's target. A destination outside the scope
// is ErrSecretDestDenied, with the route's Host naming it: it is never
// replaced by the task's target.
func (p *SecretPolicy) Admit(lk *conn.Link) (SecretRoute, error) {
	g := SecretRoute{ConnType: conn.LinkTCP, LocalProxy: p.LocalProxy}
	if lk == nil {
		return g, nil
	}
	switch lk.ConnType {
	case conn.LinkTCP, conn.LinkUDP, conn.LinkUDP5:
	default:
		return g, ErrSecretLinkRefused
	}
	if lk.Host != "" {
		g.Host = netaddr.FormatAddress(lk.Host)
	}
	if !p.NameDest {
		if g.Host != "" && !p.isTarget(g.Host) {
			return g, ErrSecretDestDenied
		}
		switch p.TargetType {
		case model.TargetAll, "":
			if lk.ConnType == conn.LinkUDP {
				g.ConnType = conn.LinkUDP
			}
		default:
			g.ConnType = p.TargetType
		}
		return g, nil
	}
	g.ConnType = lk.ConnType
	if !p.networkAllowed(lk.ConnType) || g.Host != "" && p.allows != nil && !p.allows(g.Host) {
		return g, ErrSecretDestDenied
	}
	if p.AllowLocal {
		g.LocalProxy = lk.LocalProxy
	}
	return g, nil
}

// isTarget reports whether host is one of the task's targets.
func (p *SecretPolicy) isTarget(host string) bool {
	for line := range strings.Lines(p.Targets) {
		if t := strings.TrimSpace(line); t != "" && netaddr.FormatAddress(t) == host {
			return true
		}
	}
	return false
}

// networkAllowed reports whether a named destination may use network: the
// task's target type bounds it.
func (p *SecretPolicy) networkAllowed(network string) bool {
	switch p.TargetType {
	case model.TargetTCP:
		return network == conn.LinkTCP
	case model.TargetUDP:
		return network != conn.LinkTCP
	}
	return true
}

// SecretServer relays the links of one grant (an attached visitor) by the
// grant's policy, resolved once.
type SecretServer struct {
	*BaseServer
	policy *SecretPolicy
}

func NewSecretServer(bridge NetBridge, task *model.Tunnel, policy *SecretPolicy, allowLocalProxy bool) *SecretServer {
	return &SecretServer{BaseServer: NewBaseServer(bridge, task, allowLocalProxy), policy: policy}
}

// SecretDenied writes the tunnel_denied line of a link of a visitor from
// src to t whose destination target the scope refuses.
func SecretDenied(src net.Addr, t *model.Tunnel, target string) {
	slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", srcIP(src), "route", taskName(t), "tunnel", t.Id,
		"client", clientOf(t), "target", target, "reason", "dest_acl")
}

// Serve relays src, a visitor's stream that carried lk, until either side
// ends; ack answers a link that asks for an acknowledgement, once the
// target is reached. src is closed when Serve returns.
func (s *SecretServer) Serve(src net.Conn, lk *conn.Link, ack func(net.Conn, time.Duration) error) error {
	g, err := s.policy.Admit(lk)
	if err != nil {
		if errors.Is(err, ErrSecretDestDenied) {
			SecretDenied(src.RemoteAddr(), s.Task, g.Host)
		}
		_ = src.Close()
		return err
	}
	release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
	if err != nil {
		_ = src.Close()
		return err
	}
	defer release()

	needAck := lk != nil && lk.Option.NeedAck
	route := s.Task.Route()
	connType, host, localProxy := g.ConnType, g.Host, g.LocalProxy
	var link *conn.Link
	var target net.Conn
	dial := func(addr string) (bool, error) {
		link = conn.NewLink(connType, addr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, src.RemoteAddr().String(), localProxy)
		link.Option.Node = route.Node
		var err error
		target, err = s.Bridge.Session(s.Task.Client.Id).OpenLink(link, s.Task.Id)
		return localProxy, err
	}
	if host == "" {
		err = route.Target.Dial(dial)
		if errors.Is(err, model.ErrNoTarget) {
			_, err = dial("")
		}
	} else {
		_, err = dial(host)
	}
	if err != nil {
		_ = src.Close()
		target := host
		if target == "" {
			target = route.Target.TargetStr
		}
		relayFailed(s.Task, target, err)
		return err
	}

	if needAck {
		if err := ack(src, link.Option.Timeout); err != nil {
			log.Debug("ack not written", "src_ip", srcIP(src.RemoteAddr()), "err", err)
			_ = src.Close()
			_ = target.Close()
			return err
		}
		trace("ack sent", "src_ip", srcIP(src.RemoteAddr()))
	}

	// The target is the backend side: the PROXY header goes to it.
	// Encryption and compression only apply to links through the client and
	// are skipped for local ones.
	proxyProtocol := route.Target.ProxyProtocol
	visitor := src
	if connType == conn.LinkUDP {
		// The visitor stream carries length-framed datagrams, where a single
		// stream-level PROXY header would be taken for a frame.
		proxyProtocol = 0
		if localProxy {
			// The target is a UDP socket: unframe on the visitor side.
			// CopyWaitGroup closes visitor.
			//noinspection GoResourceLeak
			visitor = netx.NewFramedConn(src, netx.WithFramedDatagrams())
		}
	}
	start := time.Now()
	out, in := conn.CopyWaitGroup(target, visitor, link.Crypt, link.Compress, s.Task.Client.Rate, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Bridge.LinkCert(), proxyProtocol, nil, s.Task, localProxy, false)
	connClosed(s.Task, src.RemoteAddr(), link.Host, in, out, start)
	return nil
}
