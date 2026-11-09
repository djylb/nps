package bridge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/byterate"
	"github.com/djylb/holepunch/coord"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/slogx"
)

// accessGuessBurst and accessGuessPerSecond bound the wrong access keys a
// client may try: past the burst its guesses are refused unchecked and not
// logged, so neither guessing nor the audit line scales with what it sends.
const (
	accessGuessBurst     = 20
	accessGuessPerSecond = 1
)

// guessBucket is one client's allowance of wrong access keys.
type guessBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (b *guessBucket) refill(now time.Time) {
	b.tokens = min(accessGuessBurst, b.tokens+now.Sub(b.last).Seconds()*accessGuessPerSecond)
	b.last = now
}

// guessesLeft reports whether client may try another access key.
func (s *Bridge) guessesLeft(client int) bool {
	v, ok := s.accessGuesses.Load(client)
	if !ok {
		return true
	}
	b := v.(*guessBucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(time.Now())
	return b.tokens >= 1
}

// spendGuess takes one wrong key off client's allowance.
func (s *Bridge) spendGuess(client int) {
	v, _ := s.accessGuesses.LoadOrStore(client, &guessBucket{tokens: accessGuessBurst, last: time.Now()})
	b := v.(*guessBucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(time.Now())
	b.tokens--
}

func accessRefused(client int, src netip.AddrPort, extra ...any) {
	args := append([]any{"src_ip", src.Addr().Unmap().String(), "src_port", int(src.Port()), "role", "secret", "client", client}, extra...)
	slogx.Emit(context.Background(), log, &logevent.AuthFailed, args...)
}

// coreResolveLink authorizes a visitor's LINK by its proof of the task's
// password (§9.1). It is the one place the core protocol reads a secret task;
// the task is checked again on every PIPE and PUNCH through the grant's Valid.
func (s *Bridge) coreResolveLink(client int, r coreapi.LinkResolve) (*coreapi.LinkGrant, error) {
	refused := coreapi.LinkNotAllowed()
	if len(r.AccessID) == 0 || !s.guessesLeft(client) {
		return nil, refused
	}
	accessID := slices.Clone(r.AccessID)
	find := func() *model.Tunnel {
		t := s.store.GetTaskByAccessID(accessID)
		if t == nil || t.View().Mode != "secret" {
			return nil
		}
		return t
	}
	t := find()
	if t == nil || !r.Verify([]byte(t.View().Password)) {
		s.spendGuess(client)
		accessRefused(client, r.Source, "reason", "bad_key")
		return nil, refused
	}
	if !t.Status || t.Client == nil {
		accessRefused(client, r.Source, "tunnel", t.Id, "route", t.LogRoute(), "reason", "disabled")
		return nil, refused
	}
	feature := servercfg.Current().Feature
	p := proxy.NewSecretPolicy(t, t.Route().Target, feature.AllowLocalProxy)
	// The server is the exit when the tunnel is served by it, or when the
	// visitor asked for it and the tunnel lets visitors (proxy to local).
	// A visitor asking without that goes to the provider.
	exit := p.LocalProxy || r.ServerExit && p.AllowLocal
	if r.ServerExit && !exit {
		log.Debug("local_proxy ignored: the tunnel does not proxy to local", "tunnel", t.Id, "client", client)
	}
	server := proxy.NewSecretServer(s, t, p, feature.AllowLocalProxy)
	// The LINK is admitted as a link would be; each PIPE then holds its slot.
	release, err := server.AcquireForwarding(t.Client, t.ControlGroup())
	if err != nil {
		return nil, refused
	}
	release()
	id := t.Id
	return &coreapi.LinkGrant{
		Tunnel:     t.Id,
		Provider:   t.Client.Id,
		ServerExit: exit,
		Policy:     linkPolicyOf(p),
		Valid: func() bool {
			c := find()
			if c == nil || c.Id != id || !c.Status || c.Client == nil {
				return false
			}
			return !flowOver(c.Flow) && !flowOver(c.Client.Flow)
		},
		ServeExit: func(_ context.Context, c net.Conn, network, target string, ack func() error) error {
			lk := conn.NewLink(exitLinkType(network), target, false, false, c.RemoteAddr().String(), true)
			lk.Option.NeedAck = true
			err := server.Serve(c, lk, func(net.Conn, time.Duration) error { return ack() })
			if errors.Is(err, proxy.ErrSecretDestDenied) || errors.Is(err, proxy.ErrSecretLinkRefused) || errors.Is(err, proxy.ErrForwardingRefused) {
				return coreapi.LinkNotAllowed()
			}
			return err
		},
		Admit: func(stop func()) (func(), error) {
			release, err := server.AcquireForwarding(t.Client, t.ControlGroup())
			if err != nil {
				return nil, err
			}
			unwatch := watchExpiry(stop, t.Flow, t.Client.Flow)
			return func() { unwatch(); release() }, nil
		},
		AddDirect: func(in, out uint64) { t.Flow.AddDirect(clampInt64(in), clampInt64(out)) },
		Limiters: func() (coreapi.Limiter, coreapi.Limiter) {
			g := t.ControlGroup()
			flows := []*model.Flow{t.Flow, t.Client.Flow}
			// The directions are the visitor's: its PIPE stands where the
			// connection to an npc stands in a relay.
			return newPipeLimiter(g.Writes(), flows, true), newPipeLimiter(g.Reads(), flows, false)
		},
	}, nil
}

// clampInt64 is n as the int64 a Flow counts in.
func clampInt64(n uint64) int64 { return int64(min(n, math.MaxInt64)) }

func flowOver(f *model.Flow) bool {
	traffic, expired := f.Over()
	return traffic || expired
}

// watchExpiry calls stop when one of flows expires, until the function it
// returns is called.
func watchExpiry(stop func(), flows ...*model.Flow) func() {
	var stops []func() bool
	for _, f := range flows {
		if b := f.Budget(); b != nil {
			stops = append(stops, b.AfterExpiry(stop))
		}
	}
	return func() {
		for _, s := range stops {
			s()
		}
	}
}

// pipeLimiter is the rate limits, the quota and the counting of a relay
// (conn.CopyWaitGroup) for bytes the protocol forwards itself: charged as
// they pass, so a long PIPE counts while it runs.
type pipeLimiter struct {
	limit    byterate.Limiter
	counters []*byterate.Counter
	inbound  bool
}

func newPipeLimiter(rate byterate.Limiter, flows []*model.Flow, inbound bool) *pipeLimiter {
	limiters := []byterate.Limiter{rate}
	l := &pipeLimiter{inbound: inbound}
	for _, f := range flows {
		if b := f.Budget(); b != nil {
			limiters = append(limiters, b)
			l.counters = append(l.counters, f.Traffic())
		}
	}
	l.limit = byterate.NewHierarchy(limiters...)
	return l
}

func (l *pipeLimiter) Ready(ctx context.Context, _, maxBytes int) (int, error) {
	n, err := l.limit.Ready(ctx, int64(maxBytes))
	return int(n), err
}

func (l *pipeLimiter) TryReady(maxBytes int) int {
	if l.limit.TryWait(int64(maxBytes)) != nil {
		return 0
	}
	l.limit.Refund(int64(maxBytes))
	return maxBytes
}

func (l *pipeLimiter) Charge(n int) {
	for _, c := range l.counters {
		if l.inbound {
			c.Add(0, int64(n))
		} else {
			c.Add(int64(n), 0)
		}
	}
	// A refusal comes back from the next Ready.
	_ = l.limit.Charge(int64(n))
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
			return "deny *\n"
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
