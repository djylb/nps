package bridge

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/holepunch/coord"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/servercfg"
)

// coreResolveLink authorizes a visitor's LINK by the task's password (§9.1).
// It is the one place the core protocol reads a secret task; the check runs
// again on every PIPE and PUNCH through the grant's Valid.
func (s *Bridge) coreResolveLink(_ int, r coreapi.LinkResolve) (*coreapi.LinkGrant, error) {
	refused := coreapi.LinkNotAllowed()
	if len(r.Access) == 0 {
		return nil, refused
	}
	password := string(r.Access)
	find := func() *model.Tunnel {
		t := s.store.GetTaskBySecretIndex(crypt.SecretIndex(password))
		if t == nil || t.Mode != "secret" || !t.Status || t.Client == nil ||
			subtle.ConstantTimeCompare([]byte(t.Password), r.Access) != 1 {
			return nil
		}
		return t
	}
	t := find()
	if t == nil {
		return nil, refused
	}
	feature := servercfg.Current().Feature
	p := proxy.NewSecretPolicy(t, t.Route().Target, feature.AllowLocalProxy, feature.AllowSecretLocal, feature.LegacySecretLink)
	server := proxy.NewSecretServer(s, t, p, feature.AllowLocalProxy)
	id := t.Id
	return &coreapi.LinkGrant{
		Tunnel:     t.Id,
		Provider:   t.Client.Id,
		ServerExit: p.LocalProxy,
		Policy:     linkPolicyOf(p),
		Valid: func() bool {
			c := find()
			return c != nil && c.Id == id
		},
		ServeExit: func(_ context.Context, c net.Conn, network, target string) {
			_ = server.Serve(c, conn.NewLink(exitLinkType(network), target, false, false, c.RemoteAddr().String(), p.LocalProxy),
				func(net.Conn, time.Duration) error { return nil })
		},
		Count: func(in, out int64) {
			t.Flow.Add(in, out)
			t.Client.Flow.Add(in, out)
		},
	}, nil
}

func exitLinkType(network string) string {
	switch network {
	case coreapi.NetworkUDP:
		return conn.LinkUDP
	case coreapi.NetworkUDP5:
		return conn.LinkUDP5
	}
	return conn.LinkTCP
}

// linkPolicyOf is p as the provider receives it (§9.1, §9.5).
func linkPolicyOf(p *proxy.SecretPolicy) coreapi.LinkPolicy {
	out := coreapi.LinkPolicy{NameDest: p.NameDest}
	switch p.TargetType {
	case model.TargetTCP:
		out.TargetType = coreapi.NetworkTCP
	case model.TargetUDP:
		out.TargetType = coreapi.NetworkUDP
	}
	for line := range strings.Lines(p.Targets) {
		if t := strings.TrimSpace(line); t != "" {
			if !strings.Contains(t, ":") {
				t = "127.0.0.1:" + t
			}
			out.Targets = append(out.Targets, t)
		}
	}
	out.Scope = scopeOfACL(p.DestAclMode, p.DestAclRules)
	return out
}

// scopeOfACL writes a task's destination list as rule text (§9.5).
func scopeOfACL(mode int, rules string) string {
	if mode != model.AclWhitelist && mode != model.AclBlacklist {
		return ""
	}
	var b strings.Builder
	for line := range strings.Lines(rules) {
		r := strings.TrimSpace(line)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		if mode == model.AclBlacklist {
			b.WriteString("deny ")
		}
		b.WriteString(r)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		if mode == model.AclWhitelist {
			return "deny 0.0.0.0/0\ndeny ::/0\n"
		}
		return ""
	}
	return b.String()
}

// coreCoordinate runs one punch of the core protocol over its control
// streams, on the same coordinator as the old protocol's.
func (s *Bridge) coreCoordinate(ctx context.Context, p coreapi.PunchPair) {
	defer func() {
		_ = p.Visitor.Close()
		_ = p.Provider.Close()
	}()
	svc := s.p2p.Load()
	if svc == nil {
		return
	}
	opts := coord.SessionOptions{
		ClientKeys:     [2]string{"client:" + strconv.Itoa(p.VisitorID), "client:" + strconv.Itoa(p.ProviderID)},
		InitiatorProbe: svc.spec(conn.NewConn(p.Visitor), p.ServerAddr, true),
		ResponderProbe: svc.spec(conn.NewConn(p.Provider), "", true),
		Labels: map[string]string{
			"task":     strconv.Itoa(p.Tunnel),
			"visitor":  strconv.Itoa(p.VisitorID),
			"provider": strconv.Itoa(p.ProviderID),
		},
	}
	if p.Background {
		opts.Mode = protocol.ModeBackground
	}
	cs, err := svc.co.NewSession(opts)
	if err != nil {
		log.Debug("punch not started", "tunnel", p.Tunnel, "err", err)
		return
	}
	if err := attachBoth(cs, p); err != nil {
		log.Debug("punch not attached", "tunnel", p.Tunnel, "err", err)
		cs.Abort(protocol.ReasonPeerOffline, err.Error())
		return
	}
	select {
	case <-cs.Done():
	case <-ctx.Done():
	}
}

func attachBoth(cs *coord.Session, p coreapi.PunchPair) error {
	if err := cs.Attach(protocol.RoleInitiator, p.Visitor, coord.AttachOptions{Security: protocol.ControlSecurityDeclared}); err != nil {
		return fmt.Errorf("visitor: %w", err)
	}
	if err := cs.Attach(protocol.RoleResponder, p.Provider, coord.AttachOptions{Security: protocol.ControlSecurityDeclared}); err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	return nil
}
