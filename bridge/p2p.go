package bridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch/coord"
	"github.com/djylb/holepunch/natprobe"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/djylb/nps/lib/version"
)

const (
	// p2pHandshakeTimeout bounds reading a P2P request and the TLS handshake
	// that follows it.
	p2pHandshakeTimeout = 10 * time.Second
	// p2pJoinTimeout bounds the wait for the invited provider's join.
	p2pJoinTimeout = 10 * time.Second
	// p2pWatchStop bounds the end of the visitor-stream watch of the join
	// wait once its read deadline is set.
	p2pWatchStop = time.Second
	// p2pSTUNRefresh is how often the STUN servers' names are resolved again,
	// p2pSTUNRetry while the last resolution found none.
	p2pSTUNRefresh = 6 * time.Hour
	p2pSTUNRetry   = time.Minute
)

// P2PConfig configures the bridge's P2P service: the NAT observers and the
// coordinator that pairs a visitor with a provider.
type P2PConfig struct {
	// BasePort is the first of the observers' three UDP ports; they listen
	// on 0.0.0.0 and, when the host allows it, on ::. 0 lets the kernel
	// choose.
	BasePort int
	// IP is p2p_ip: the addresses (IPs or host names, comma separated)
	// announced to the clients; 0.0.0.0 and :: detect the host's IPv4 and
	// IPv6 address.
	IP          string
	STUNServers []string          // host:port of public STUN servers handed to the clients; empty: none
	Timeouts    protocol.Timeouts // zero fields take the library defaults
	// PortMapping enables the clients' port mapping methods.
	PortMapping protocol.PortMappingPolicy
}

// p2pService runs the P2P sessions of a bridge.
type p2pService struct {
	probe    *natprobe.Server
	co       *coord.Coordinator
	tlsCfg   *tls.Config
	groups   []natprobe.ObserverGroup // natprobe's groups by index
	announce *p2pAnnouncer
	stun     atomic.Pointer[[]netip.AddrPort]
	ctx      context.Context // ends when the service closes
	cancel   context.CancelFunc
	wg       sync.WaitGroup // the STUN refresher

	succeeded, failed atomic.Int64

	mu       sync.Mutex
	invites  map[string]*p2pInvite // by invitation id, while the visitor waits
	nodes    map[string]*p2pBucket // request budget by visitor node (client id and npc UUID)
	clients  map[int]*p2pBucket    // request budget by visitor client id, all its nodes together
	inflight map[int]int           // requests and sessions in progress by visitor client id
}

// P2P request budgets: a visitor node (one npc) may make p2pNodeBurst
// requests at once, refilled at p2pNodeRate per second; all nodes of one
// client together p2pClientBurst at p2pClientRate, with at most
// p2pClientInflight requests and sessions in progress. An npc backs off
// between attempts; these bound clients that do not, wrong passwords
// included, while devices sharing a vkey do not crowd each other out.
const (
	p2pNodeRate       = 1.0
	p2pNodeBurst      = 10.0
	p2pClientRate     = 5.0
	p2pClientBurst    = 30.0
	p2pClientInflight = 32
)

type p2pBucket struct {
	tokens float64
	at     time.Time
}

// take takes a token from b, refilled at rate up to burst.
func (b *p2pBucket) take(now time.Time, rate, burst float64) bool {
	b.tokens = min(burst, b.tokens+now.Sub(b.at).Seconds()*rate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// admit takes a request of a visitor node from the budgets; done releases
// it when it ends without a session (a session releases it in onResult).
func (p *p2pService) admit(clientID int, nodeUUID string) bool {
	now := time.Now()
	key := strconv.Itoa(clientID) + "/" + nodeUUID
	p.mu.Lock()
	defer p.mu.Unlock()
	nb := p.nodes[key]
	if nb == nil {
		nb = &p2pBucket{tokens: p2pNodeBurst, at: now}
		p.nodes[key] = nb
	}
	cb := p.clients[clientID]
	if cb == nil {
		cb = &p2pBucket{tokens: p2pClientBurst, at: now}
		p.clients[clientID] = cb
	}
	if p.inflight[clientID] >= p2pClientInflight || !nb.take(now, p2pNodeRate, p2pNodeBurst) || !cb.take(now, p2pClientRate, p2pClientBurst) {
		return false
	}
	p.inflight[clientID]++
	return true
}

// done ends a request or session of the visitor client.
func (p *p2pService) done(clientID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight[clientID]--; p.inflight[clientID] <= 0 {
		delete(p.inflight, clientID)
	}
}

// p2pInvite is an invitation waiting for its provider's join.
type p2pInvite struct {
	providerID int
	token      string
	joined     bool
	join       chan p2pJoined // capacity 1: the claiming join hands its stream over
}

// p2pJoined is the provider's answer: its control stream, the server address
// it reported and whether that address may be used, or its refusal.
type p2pJoined struct {
	c        *conn.Conn
	serverIP string
	trusted  bool
	decline  string
}

var (
	errP2PUnknownInvite = errors.New("unknown or expired invitation")
	errP2PBadToken      = errors.New("join token mismatch")
	errP2PJoined        = errors.New("invitation already joined")
)

// StartP2P starts the NAT observers and the coordinator. It must be called
// before the bridge serves clients; without it P2P requests are refused.
func (s *Bridge) StartP2P(cfg P2PConfig) error {
	svc, err := newP2PService(cfg)
	if err != nil {
		return err
	}
	s.p2p.Store(svc)
	return nil
}

// StopP2P closes the P2P service; running sessions end.
func (s *Bridge) StopP2P() {
	if svc := s.p2p.Load(); svc != nil {
		svc.close()
	}
}

func newP2PService(cfg P2PConfig) (*p2pService, error) {
	cert := crypt.GetCert()
	if len(cert.Certificate) == 0 {
		return nil, errors.New("p2p: no bridge certificate")
	}
	reg, probe, groups, err := startObservers(cfg.BasePort, cfg.IP, p2pLogger())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := &p2pService{
		probe:    probe,
		tlsCfg:   &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		groups:   groups,
		announce: newP2PAnnouncer(cfg.IP),
		ctx:      ctx,
		cancel:   cancel,
		invites:  make(map[string]*p2pInvite),
		nodes:    make(map[string]*p2pBucket),
		clients:  make(map[int]*p2pBucket),
		inflight: make(map[int]int),
	}
	svc.co, err = coord.New(coord.Config{
		Registry:     reg,
		DefaultProbe: probe.ProbeSpec(),
		Timeouts:     cfg.Timeouts,
		Policy:       &protocol.Policy{PortMapping: cfg.PortMapping},
		OnResult:     svc.onResult,
	})
	if err != nil {
		cancel()
		_ = probe.Close()
		return nil, err
	}
	svc.wg.Go(func() { svc.refreshSTUN(ctx, cfg.STUNServers) })
	return svc, nil
}

// startObservers starts natprobe on UDP basePort..basePort+2 of the
// wildcard addresses of both families, or of IPv4 alone when the host has no
// IPv6. Binding the wildcard follows an address that changes at run time (a
// dynamic public address on the host's interface). Without IP_PKTINFO a
// reply leaves from the address the host routes it from: on a host with
// several addresses of a family that is the source address towards the
// client, which the announcer therefore prefers (p2pAnnouncer), and the
// configuration is logged. The groups returned are natprobe's, by index.
func startObservers(basePort int, p2pIP string, logger *slog.Logger) (*natprobe.Registry, *natprobe.Server, []natprobe.ObserverGroup, error) {
	v4 := natprobe.ObserverGroup{Bind: netip.IPv4Unspecified(), Advertise: p2pScopeHint(netip.IPv4Unspecified(), p2pIP)}
	v6 := natprobe.ObserverGroup{Bind: netip.IPv6Unspecified(), Advertise: p2pScopeHint(netip.IPv6Unspecified(), p2pIP)}
	for f, addrs := range hostAddrs() {
		if len(addrs) > 1 {
			logs.Warn("P2P: this host has %d %s addresses %v; observer replies leave from the address the host routes to each client, which is the address announced unless p2p_ip names one",
				len(addrs), p2pFamilyNames[f], addrs)
		}
	}
	var dualErr error
	for _, groups := range [][]natprobe.ObserverGroup{{v4, v6}, {v4}} {
		reg := natprobe.NewRegistry(natprobe.RegistryConfig{})
		probe, err := natprobe.NewServer(natprobe.ServerConfig{Registry: reg, Groups: groups, BasePort: basePort, Logger: logger})
		if err != nil {
			return nil, nil, nil, err
		}
		if err = probe.Start(); err == nil {
			if dualErr != nil {
				logs.Info("P2P observers listen on IPv4 only: %v", dualErr)
			}
			return reg, probe, groups, nil
		}
		if dualErr != nil {
			return nil, nil, nil, err
		}
		dualErr = err
	}
	return nil, nil, nil, dualErr
}

var p2pFamilyNames = [2]string{"IPv4", "IPv6"}

// Stand-ins for the groups' Advertise, which never reaches a client: holepunch counts the first two as public, the last two as private.
var (
	p2pPublicHint4  = netip.MustParseAddr("203.0.113.1")
	p2pPublicHint6  = netip.MustParseAddr("2001:db8::1")
	p2pPrivateHint4 = netip.MustParseAddr("10.255.255.1")
	p2pPrivateHint6 = netip.MustParseAddr("fd00::1")
)

// p2pScopeHint is a stand-in address of the wildcard's family whose scope is
// the deployment's: that of p2p_ip's first explicit address of the family,
// else of the detected outbound address; public when neither is known. A
// server on a private network announces private observers, so that its
// clients are classified by their NAT rather than taken for clients on the
// server's LAN.
func p2pScopeHint(wildcard netip.Addr, p2pIP string) netip.Addr {
	f := p2pFamily(wildcard)
	var scopeOf netip.Addr
	for _, v := range strings.Split(p2pIP, ",") {
		if ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(v), "[]")); err == nil && !ip.Unmap().IsUnspecified() && p2pFamily(ip) == f {
			scopeOf = ip.Unmap()
			break
		}
	}
	if !scopeOf.IsValid() {
		if d := newP2PAnnouncer("").detect(wildcard); d.IsValid() && !d.IsLoopback() {
			scopeOf = d
		}
	}
	public := !scopeOf.IsValid() || p2pPublic(scopeOf)
	switch {
	case f == 0 && public:
		return p2pPublicHint4
	case f == 0:
		return p2pPrivateHint4
	case public:
		return p2pPublicHint6
	}
	return p2pPrivateHint6
}

// hostAddrs returns the host's global unicast addresses by family, for the
// start-up log.
func hostAddrs() [2][]netip.Addr {
	var out [2][]netip.Addr
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, ia := range addrs {
		p, err := netip.ParsePrefix(ia.String())
		if err != nil {
			continue
		}
		if a := p.Addr().Unmap(); a.IsGlobalUnicast() {
			out[p2pFamily(a)] = append(out[p2pFamily(a)], a)
		}
	}
	return out
}

// p2pLogger passes natprobe's log lines (a wildcard bind on a host with
// several addresses, stopped listeners) to the nps log.
func p2pLogger() *slog.Logger { return slog.New(p2pLogHandler{}) }

type p2pLogHandler struct{ attrs []slog.Attr }

func (p2pLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h p2pLogHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString("P2P observers: ")
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	switch {
	case r.Level >= slog.LevelError:
		logs.Error("%s", b.String())
	case r.Level >= slog.LevelWarn:
		logs.Warn("%s", b.String())
	case r.Level >= slog.LevelInfo:
		logs.Info("%s", b.String())
	default:
		logs.Debug("%s", b.String())
	}
	return nil
}

func (h p2pLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return p2pLogHandler{attrs: append(slices.Clone(h.attrs), attrs...)}
}

func (h p2pLogHandler) WithGroup(string) slog.Handler { return h }

func (p *p2pService) close() {
	p.cancel()
	p.wg.Wait()
	_ = p.co.Close()
	_ = p.probe.Close()
}

// spec is the ProbeSpec of one client: natprobe's observers announced at
// the client's first candidate address of each family, and the STUN servers
// as last resolved.
func (p *p2pService) spec(c *conn.Conn, reportedServerIP string, trusted bool) *protocol.ProbeSpec {
	spec := p.probe.ProbeSpec()
	spec.Observers = selectObservers(spec.Observers, p.groups, p.announce.candidates(p.ctx, reportedServerIP, trusted, c.RemoteAddr(), c.LocalAddr()))
	if s := p.stun.Load(); s != nil {
		spec.STUN = *s
	}
	return &spec
}

// refreshSTUN resolves the STUN servers now and again every p2pSTUNRefresh
// (p2pSTUNRetry while none resolved) until ctx ends.
func (p *p2pService) refreshSTUN(ctx context.Context, servers []string) {
	if len(servers) == 0 {
		return
	}
	for {
		addrs, err := natprobe.ResolveSTUN(ctx, nil, servers)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logs.Warn("P2P STUN servers left out: %s", strings.ReplaceAll(err.Error(), "\n", "; "))
		}
		if len(addrs) > 0 || p.stun.Load() == nil {
			p.stun.Store(&addrs)
		}
		logs.Debug("P2P STUN servers: %v", addrs)
		wait := p2pSTUNRefresh
		if len(*p.stun.Load()) == 0 {
			wait = p2pSTUNRetry
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// invite records an invitation for the provider; drop removes it.
func (p *p2pService) invite(providerID int) (id, token string, inv *p2pInvite, drop func(), err error) {
	var raw [48]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", nil, nil, err
	}
	id = base64.RawURLEncoding.EncodeToString(raw[:16])
	token = base64.RawURLEncoding.EncodeToString(raw[16:])
	inv = &p2pInvite{providerID: providerID, token: token, join: make(chan p2pJoined, 1)}
	p.mu.Lock()
	p.invites[id] = inv
	p.mu.Unlock()
	return id, token, inv, func() {
		p.mu.Lock()
		delete(p.invites, id)
		p.mu.Unlock()
		// A join handed over before the removal but never taken.
		select {
		case j := <-inv.join:
			_ = j.c.Close()
		default:
		}
	}, nil
}

// claim accepts the join of an invitation by the invited provider with its
// token, once, and hands the provider's stream to the waiting visitor. The
// hand-over happens under the lock, so that an invitation removed at the
// same time either never gets the stream or finds it when it is removed.
func (p *p2pService) claim(id string, providerID int, token string, j p2pJoined) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	inv := p.invites[id]
	switch {
	case inv == nil || inv.providerID != providerID:
		return errP2PUnknownInvite
	case subtle.ConstantTimeCompare([]byte(token), []byte(inv.token)) != 1:
		return errP2PBadToken
	case inv.joined:
		return errP2PJoined
	}
	inv.joined = true
	inv.join <- j // capacity 1, sent once
	return nil
}

func (p *p2pService) onResult(r coord.Result) {
	task, visitor, provider := r.Labels["task"], r.Labels["visitor"], r.Labels["provider"]
	if id, err := strconv.Atoi(visitor); err == nil {
		p.done(id)
	}
	if r.Outcome == coord.OutcomeSucceeded {
		n := p.succeeded.Add(1)
		family := ""
		if r.Winner != nil {
			family = string(r.Winner.Family)
		}
		logs.Info("P2P session %s of task %s connected client %s to client %s directly (%s, %d rounds, pair %s; %d connected, %d failed in total)",
			r.SessionID, task, visitor, provider, family, len(r.Rounds), r.PairClass, n, p.failed.Load())
		return
	}
	n := p.failed.Add(1)
	logs.Warn("P2P session %s of task %s between client %s and client %s %s: %s %s (side %q, advice %q; %d connected, %d failed in total)",
		r.SessionID, task, visitor, provider, r.Outcome, r.Code, r.Detail, r.FailedRole, r.Retry.Advice, p.succeeded.Load(), n)
}

// handleP2PVisitor serves a WORK_P2P stream: it checks the request, invites
// the provider, waits for its join, opens the session with each side's probe
// addresses and attaches both streams, turned into TLS, to it.
func (s *Bridge) handleP2PVisitor(c *conn.Conn, id, ver int, vs, uuid, tunnelType string) {
	if !version.SupportsP2P(ver, vs) {
		logs.Error("P2P request of client %d from %v refused: npc %s (protocol %s) does not speak the P2P protocol of this server, upgrade npc to %s or newer",
			id, c.RemoteAddr(), vs, version.GetVersion(ver), version.P2PMinVersion)
		_ = c.Close()
		return
	}
	_ = c.SetDeadline(time.Now().Add(p2pHandshakeTimeout))
	var req p2pproto.Request
	if err := p2pproto.Read(c, &req); err != nil {
		logs.Warn("P2P request of client %d from %v unreadable: %v", id, c.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	svc := s.p2p.Load()
	if svc == nil {
		refuseP2P(c, id, p2pproto.CodeRefused, "P2P is disabled on this server (p2p_port is not set)")
		return
	}
	if !svc.admit(id, uuid) {
		logs.Debug("P2P request of client %d from %v dropped: too many requests", id, c.RemoteAddr())
		_ = p2pproto.Write(c, p2pproto.Reply{Code: p2pproto.CodeBusy, Message: "too many P2P requests"})
		_ = c.Close()
		return
	}
	if !s.openP2PSession(svc, c, id, uuid, tunnelType, req) {
		svc.done(id)
	}
}

// openP2PSession runs an admitted visitor request: it invites the provider,
// waits for its join, opens the session with each side's probe addresses and
// attaches both streams, turned into TLS, to it. It reports whether a
// session was opened, which releases the request in onResult.
func (s *Bridge) openP2PSession(svc *p2pService, c *conn.Conn, id int, uuid, tunnelType string, req p2pproto.Request) bool {
	t, node, code, msg := s.resolveP2PTarget(req.Password)
	if code != "" {
		refuseP2P(c, id, code, msg)
		return false
	}
	inviteID, token, inv, drop, err := svc.invite(t.Client.Id)
	if err != nil {
		refuseP2P(c, id, p2pproto.CodeBusy, err.Error())
		return false
	}
	defer drop()
	if err := node.sendSignal(common.P2P_PUNCH_START, p2pproto.Invite{SessionID: inviteID, Token: token}, p2pHandshakeTimeout); err != nil {
		refuseP2P(c, id, p2pproto.CodeOffline, fmt.Sprintf("client %d unreachable: %v", t.Client.Id, err))
		return false
	}
	joined, ok := waitJoin(svc, c, inv)
	if !ok {
		if c.IsClosed() {
			return false
		}
		refuseP2P(c, id, p2pproto.CodeOffline, fmt.Sprintf("client %d did not answer the invitation", t.Client.Id))
		return false
	}
	if joined.decline != "" {
		refuseP2P(c, id, p2pproto.CodeRefused, fmt.Sprintf("client %d declined: %s", t.Client.Id, joined.decline))
		return false
	}
	pc := joined.c
	opts := coord.SessionOptions{
		// The visitor's key is its node, so that devices sharing a vkey do
		// not share the coordinator's per-key limits.
		ClientKeys:     [2]string{"client:" + strconv.Itoa(id) + "/" + uuid, "client:" + strconv.Itoa(t.Client.Id)},
		InitiatorProbe: svc.spec(c, req.ServerIP, p2pDirect(tunnelType)),
		ResponderProbe: svc.spec(pc, joined.serverIP, joined.trusted),
		Labels: map[string]string{
			"task":     strconv.Itoa(t.Id),
			"visitor":  strconv.Itoa(id),
			"provider": strconv.Itoa(t.Client.Id),
		},
	}
	if req.Background {
		opts.Mode = protocol.ModeBackground
	}
	cs, err := svc.co.NewSession(opts)
	if err != nil {
		msg := "cannot open a session now: " + err.Error()
		refuseP2P(pc, t.Client.Id, p2pproto.CodeBusy, msg)
		refuseP2P(c, id, p2pproto.CodeBusy, msg)
		return false
	}
	logs.Debug("P2P session %s of task %d: client %d with client %d, probing %v and %v",
		cs.ID(), t.Id, id, t.Client.Id, opts.InitiatorProbe.Observers, opts.ResponderProbe.Observers)
	var wg sync.WaitGroup
	wg.Go(func() { replyAndAttach(pc, cs, protocol.RoleResponder, svc.tlsCfg) })
	replyAndAttach(c, cs, protocol.RoleInitiator, svc.tlsCfg)
	wg.Wait()
	return true
}

// waitJoin waits up to p2pJoinTimeout for the provider's join. The visitor
// sends nothing until it is answered, so a read on its stream returns only
// when the stream ends: then the wait ends at once and c is closed.
func waitJoin(svc *p2pService, c *conn.Conn, inv *p2pInvite) (p2pJoined, bool) {
	gone := make(chan struct{})
	watching := make(chan struct{})
	_ = c.SetReadDeadline(time.Time{})
	go func() {
		defer close(watching)
		var b [1]byte
		if _, err := c.Read(b[:]); !isTimeout(err) {
			close(gone) // ended, or broke the protocol
		}
	}()
	timer := time.NewTimer(p2pJoinTimeout)
	defer timer.Stop()
	var joined p2pJoined
	ok := false
	select {
	case joined = <-inv.join:
		ok = true
	case <-gone:
	case <-timer.C:
	case <-svc.ctx.Done():
	}
	_ = c.SetReadDeadline(time.Now()) // stop the watcher
	select {
	case <-watching:
	case <-time.After(p2pWatchStop):
		// The stream did not honour the deadline: the watcher might still
		// take bytes of the TLS handshake, so the stream cannot be used.
		logs.Warn("P2P: the visitor stream from %v ignored its read deadline", c.RemoteAddr())
		_ = c.Close()
		<-watching
		if ok {
			_ = joined.c.Close()
		}
		return p2pJoined{}, false
	}
	select {
	case <-gone:
		_ = c.Close()
		if ok {
			_ = joined.c.Close()
		}
		return p2pJoined{}, false
	default:
	}
	_ = c.SetDeadline(time.Now().Add(p2pHandshakeTimeout))
	return joined, ok
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// handleP2PJoin serves a WORK_P2P_SESSION stream: the provider answers an
// invitation, and the stream goes to the visitor's handler.
func (s *Bridge) handleP2PJoin(c *conn.Conn, id, ver int, vs, tunnelType string) {
	svc := s.p2p.Load()
	if svc == nil || !version.SupportsP2P(ver, vs) {
		_ = c.Close()
		return
	}
	_ = c.SetDeadline(time.Now().Add(p2pHandshakeTimeout))
	var join p2pproto.Join
	if err := p2pproto.Read(c, &join); err != nil {
		logs.Warn("P2P join of client %d from %v unreadable: %v", id, c.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	j := p2pJoined{c: c, serverIP: join.ServerIP, trusted: p2pDirect(tunnelType)}
	if join.Decline != "" {
		j.decline = join.Decline
		_ = c.Close()
	}
	if err := svc.claim(join.SessionID, id, join.Token, j); err != nil {
		refuseP2P(c, id, p2pproto.CodeRefused, fmt.Sprintf("join of invitation %q: %v", join.SessionID, err))
	}
}

// p2pDirect reports whether a bridge connection of tunnelType reaches the
// bridge directly, so that the server address its client reports is the
// bridge's own: a WebSocket bridge may sit behind a reverse proxy or CDN.
func p2pDirect(tunnelType string) bool {
	return tunnelType != common.CONN_WS && tunnelType != common.CONN_WSS
}

// resolveP2PTarget finds the P2P task of a password and its provider's
// node; code is empty when the request may proceed.
func (s *Bridge) resolveP2PTarget(passwordMd5 string) (t *file.Tunnel, node *Node, code, msg string) {
	t = file.GetDb().GetTaskByMd5Password(passwordMd5)
	switch {
	case t == nil:
		return nil, nil, p2pproto.CodeRefused, "no task has this password"
	case t.Mode != "p2p":
		return nil, nil, p2pproto.CodeRefused, fmt.Sprintf("task %d is a %s task, not a p2p task", t.Id, t.Mode)
	case !t.Status:
		return nil, nil, p2pproto.CodeRefused, fmt.Sprintf("task %d is disabled", t.Id)
	case t.Client == nil:
		return nil, nil, p2pproto.CodeRefused, fmt.Sprintf("task %d has no client", t.Id)
	}
	v, ok := s.Client.Load(t.Client.Id)
	if !ok {
		return nil, nil, p2pproto.CodeOffline, fmt.Sprintf("client %d is offline", t.Client.Id)
	}
	node = v.(*Client).GetNode()
	if node == nil || node.GetSignal() == nil {
		return nil, nil, p2pproto.CodeOffline, fmt.Sprintf("client %d is offline", t.Client.Id)
	}
	if ver, vs := node.versions(); !version.SupportsP2P(ver, vs) {
		return nil, nil, p2pproto.CodeUnsupported, fmt.Sprintf("npc %s of client %d does not speak the P2P protocol of this server, upgrade it to %s or newer",
			vs, t.Client.Id, version.P2PMinVersion)
	}
	return t, node, "", ""
}

// refuseP2P answers a P2P request with a refusal and closes the stream.
func refuseP2P(c *conn.Conn, id int, code, msg string) {
	logs.Info("P2P request of client %d from %v refused: %s", id, c.RemoteAddr(), msg)
	_ = p2pproto.Write(c, p2pproto.Reply{Code: code, Message: msg})
	_ = c.Close()
}

// replyAndAttach accepts a control stream, runs the TLS server handshake on
// it and hands the TLS stream to the session, which owns it from then on. A
// failure closes the stream and aborts the session, which cannot finish
// without this side.
func replyAndAttach(c *conn.Conn, cs *coord.Session, role protocol.Role, cfg *tls.Config) {
	_ = c.SetDeadline(time.Now().Add(p2pHandshakeTimeout))
	if err := p2pproto.Write(c, p2pproto.Reply{SessionID: cs.ID()}); err != nil {
		logs.Warn("P2P session %s: the %s at %v is gone: %v", cs.ID(), role, c.RemoteAddr(), err)
		_ = c.Close()
		cs.Abort(protocol.ReasonPeerOffline, "gone_"+string(role))
		return
	}
	tc := tls.Server(c, cfg)
	if err := tc.Handshake(); err != nil {
		logs.Warn("P2P session %s: TLS handshake with the %s at %v failed: %v", cs.ID(), role, c.RemoteAddr(), err)
		_ = tc.Close()
		cs.Abort(protocol.ReasonControlInterrupted, "tls_"+string(role))
		return
	}
	_ = c.SetDeadline(time.Time{})
	if err := cs.Attach(role, tc, coord.AttachOptions{}); err != nil {
		logs.Warn("P2P session %s: attaching the %s failed: %v", cs.ID(), role, err)
		_ = tc.Close()
		cs.Abort(protocol.ReasonControlInterrupted, "attach_"+string(role))
	}
}
