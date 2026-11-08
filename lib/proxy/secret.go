package proxy

import (
	"errors"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
)

// SecretPolicy is what a visitor of a secret task may reach. nps applies it
// to the links it relays (SecretServer) and hands it to the provider npc in
// the invitation of a direct path, where the provider applies it to every
// link the visitor sends over that path: both paths follow the same rules.
type SecretPolicy struct {
	// Targets are the task's target addresses, one per line, used when the
	// visitor names none or may not name one.
	Targets string `json:"targets,omitempty"`
	// TargetType is the task's network for its targets: tcp, udp or all.
	TargetType string `json:"target_type,omitempty"`
	// AllowLink lets the visitor choose the destination and the network
	// (allow_secret_link).
	AllowLink bool `json:"allow_link,omitempty"`
	// AllowLocal lets a visitor with AllowLink choose whether nps reaches
	// the destination itself (allow_secret_local and allow_local_proxy).
	AllowLocal bool `json:"allow_local,omitempty"`
	// LocalProxy is whether nps reaches the target itself when the visitor
	// does not choose: then no direct path can serve the link.
	LocalProxy bool `json:"local_proxy,omitempty"`
	// DestAclMode and DestAclRules are the task's destination list, applied
	// to destinations the visitor chooses.
	DestAclMode  int    `json:"dest_acl_mode,omitempty"`
	DestAclRules string `json:"dest_acl_rules,omitempty"`

	allows func(addr string) bool
}

// SecretGrant is what an admitted link reaches: Host empty means one of
// the policy's targets.
type SecretGrant struct {
	ConnType   string
	Host       string
	LocalProxy bool
}

// ErrSecretLinkRefused is a link the policy does not admit.
var ErrSecretLinkRefused = errors.New("secret link refused by the task's policy")

// NewSecretPolicy is the policy of task t reaching target, with the
// server's allow_local_proxy, allow_secret_link and allow_secret_local.
func NewSecretPolicy(t *model.Tunnel, target *model.Target, allowLocalProxy, allowLink, allowLocal bool) *SecretPolicy {
	p := &SecretPolicy{
		TargetType:   t.TargetType,
		AllowLink:    allowLink,
		AllowLocal:   allowLink && allowLocal && allowLocalProxy,
		DestAclMode:  t.DestAclMode,
		DestAclRules: t.DestAclRules,
		allows:       t.AllowsDestination,
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
	p.allows = d.Allows
	return nil
}

// Direct reports whether a direct path can serve some link: not when nps
// must reach the target itself.
func (p *SecretPolicy) Direct() bool {
	return !p.LocalProxy || p.AllowLocal
}

// Admit decides what lk reaches; lk nil is a visitor that sent no link
// information.
func (p *SecretPolicy) Admit(lk *conn.Link) (SecretGrant, error) {
	g := SecretGrant{ConnType: conn.LinkTCP, LocalProxy: p.LocalProxy}
	if lk == nil {
		return g, nil
	}
	if p.AllowLink {
		switch lk.ConnType {
		case conn.LinkTCP, conn.LinkUDP, conn.LinkUDP5:
			g.ConnType = lk.ConnType
		default:
			return g, ErrSecretLinkRefused
		}
		if lk.Host != "" {
			g.Host = netaddr.FormatAddress(lk.Host)
			if p.allows != nil && !p.allows(g.Host) {
				return g, errProxyAccessDenied
			}
		}
		if p.AllowLocal {
			g.LocalProxy = lk.LocalProxy
		}
		return g, nil
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

type SecretServer struct {
	*BaseServer
	allowSecretLink  bool
	allowSecretLocal bool
}

func NewSecretServer(bridge NetBridge, task *model.Tunnel, allowLocalProxy, allowSecretLink, allowSecretLocal bool) *SecretServer {
	return &SecretServer{
		BaseServer:       NewBaseServer(bridge, task, allowLocalProxy),
		allowSecretLink:  allowSecretLink,
		allowSecretLocal: allowSecretLocal,
	}
}

// HandleSecret serves a relayed link of a visitor by the task's SecretPolicy.
func (s *SecretServer) HandleSecret(src net.Conn, wire conn.SecretWire) error {
	release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
	if err != nil {
		_ = src.Close()
		return err
	}
	defer release()

	var rb []byte
	tee := netx.NewRecordingConn(src, 0)
	// c wraps src only; it is closed on failure and CopyWaitGroup closes it otherwise.
	//noinspection GoResourceLeak
	c := conn.NewConn(tee)
	lk, err := wire.ReadLink(c)
	if err != nil || lk == nil {
		lk = nil
		rb = tee.Buffered()
	}
	tee.Discard()

	needAck := lk != nil && lk.Option.NeedAck
	route := s.Task.Route()
	g, err := NewSecretPolicy(s.Task, route.Target, s.AllowLocalProxy, s.allowSecretLink, s.allowSecretLocal).Admit(lk)
	if err != nil {
		if errors.Is(err, errProxyAccessDenied) {
			destDenied(src.RemoteAddr(), s.Task, g.Host)
		}
		_ = c.Close()
		return err
	}
	connType, host, localProxy := g.ConnType, g.Host, g.LocalProxy
	var link *conn.Link
	var target net.Conn
	dial := func(addr string) (bool, error) {
		link = conn.NewLink(connType, addr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, c.Conn.RemoteAddr().String(), localProxy)
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
		_ = c.Close()
		target := host
		if target == "" {
			target = route.Target.TargetStr
		}
		relayFailed(s.Task, target, err)
		return err
	}

	if needAck {
		if err := wire.WriteAck(c.Conn, link.Option.Timeout); err != nil {
			log.Debug("ack not written", "src_ip", srcIP(src.RemoteAddr()), "err", err)
			_ = c.Close()
			_ = target.Close()
			return err
		}
		trace("ack sent", "src_ip", srcIP(src.RemoteAddr()))
	}

	// The target is the backend side: the PROXY header and the bytes read
	// while probing for link information go to it. Encryption and
	// compression only apply to links through the client and are skipped
	// for local ones.
	proxyProtocol := route.Target.ProxyProtocol
	visitor := c.Conn
	if connType == conn.LinkUDP {
		// The visitor stream carries length-framed datagrams, where a single
		// stream-level PROXY header would be taken for a frame.
		proxyProtocol = 0
		if localProxy {
			// The target is a UDP socket: unframe on the visitor side.
			// CopyWaitGroup closes visitor.
			//noinspection GoResourceLeak
			visitor = netx.NewFramedConn(c.Conn, netx.WithFramedDatagrams())
		}
	}
	start := time.Now()
	out, in := conn.CopyWaitGroup(target, visitor, link.Crypt, link.Compress, s.Task.Client.Rate, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Bridge.LinkCert(), proxyProtocol, rb, s.Task, localProxy, false)
	connClosed(s.Task, c.RemoteAddr(), link.Host, in, out, start)
	return nil
}
