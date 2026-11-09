package session

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxquic"
	"github.com/zmuxio/zmux-go/zmuxws"
)

var log = slogx.Module("core")

// legacyMagic starts a carrier of the old protocol.
const legacyMagic = "TST"

// legacySubprotocol is the WebSocket subprotocol that, on the bridge path,
// marks the core protocol while the old protocol still listens there (§12).
const legacySubprotocol = "binary"

const (
	drainTimeout = 5 * time.Second
	// Shared bridge ports see many short carriers of old clients per source.
	maxPending          = 4096
	maxPendingPerSource = 64
	// sessionMaxPipes is what the entry aims for per session. It is no
	// policy (client_max_pipes is), only what zmux sizes its join replay
	// window by.
	sessionMaxPipes = 1024
	// defaultResumeTimeout is the grace of a session that lost its pipes
	// (§3.8) on both sides.
	defaultResumeTimeout = 30 * time.Second
	// maxSuspended is no policy: the memory budget of the listener is what
	// bounds suspended sessions, and a network failure of the server may
	// suspend every client at once.
	maxSuspended = 1 << 30
	// Stalled carriers hold a socket for up to a couple of minutes and are
	// the cheapest thing to open, so they get what the admission gives
	// pending carriers, per source too; past that a probe gets a close
	// rather than the file descriptors of real clients.
	maxStalled          = maxPending
	maxStalledPerSource = maxPendingPerSource
)

// resumeTimeoutOf is a configured grace: 0 is the default, negative no resume.
func resumeTimeoutOf(d time.Duration) time.Duration {
	switch {
	case d == 0:
		return defaultResumeTimeout
	case d < 0:
		return 0
	}
	return d
}

type carrierKey struct{}

// carrierCtx is what the fallback needs of its carrier.
type carrierCtx struct {
	label string
	tls   *tls.Conn
	other func(net.Conn, *tls.ConnectionState) bool // ServeMixed
}

// Acceptor is the core protocol entry of the server's bridge ports.
type Acceptor struct {
	table *handleTable
	zl    *zmux.Listener
	hooks atomic.Pointer[coreapi.Hooks]
	key   *ecdh.PrivateKey
	hello []byte

	sessionOpts wire.SessionOptions
	limits      CompressLimits
	maxPipes    int // client_max_pipes, 0 unlimited
	shaping     atomic.Pointer[shapingPlan]
	shapingFrom func(context.Context, func([]byte))

	linkMu sync.Mutex
	links  map[uint64]*peerLink

	mu     sync.Mutex
	sess   map[int][]*serverSession
	grants map[int][]*pipeGrant // client_max_pipes: pipes admitted, not yet counted

	stallMu                  sync.Mutex
	stalled                  map[netip.Prefix]int
	stallTotal               int
	stallMax, stallPerSource int

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Options are the settings of the entry that are not per client.
type Options struct {
	// Session is what every snapshot carries as session_options (§5).
	Session wire.SessionOptions
	// Compress are the receive limits of compressed streams (§7.4).
	Compress CompressLimits
	// MaxLanes is session_max_lanes: 0 is DefaultMaxLanes, 1 no lanes.
	MaxLanes int
	// MaxPipes limits the pipes of one client over all its sessions
	// (client_max_pipes); 0 is no limit.
	MaxPipes int
	// ResumeTimeout is how long a session that lost all its pipes waits for
	// one (§3.8); 0 is 30 s, negative is no resume.
	ResumeTimeout time.Duration
	// ShapingSource keeps the bundle current until ctx ends, through set; nil
	// is none (§13).
	ShapingSource func(ctx context.Context, set func([]byte))
}

// NewAcceptor builds the entry over store, with a key of this process
// (nothing is persisted, §3.3).
func NewAcceptor(store Store, opts Options) (*Acceptor, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	a := &Acceptor{links: map[uint64]*peerLink{}, table: newHandleTable(store), key: key, hello: serverHello(), sess: map[int][]*serverSession{},
		grants: map[int][]*pipeGrant{}, sessionOpts: opts.Session, limits: opts.Compress.normalized(), maxPipes: opts.MaxPipes, shapingFrom: opts.ShapingSource,
		stalled: map[netip.Prefix]int{}, stallMax: maxStalled, stallPerSource: maxStalledPerSource}
	base := &zmux.Config{AllowRecordMode: a.allowJoinMode, AuthorizeJoin: a.authorizeJoin, MaxLanes: maxLanesOf(opts.MaxLanes),
		EnableDatagrams: true, MaxPipes: sessionMaxPipes, ResumeTimeout: resumeTimeoutOf(opts.ResumeTimeout)}
	a.zl, err = zmux.NewListener(&zmux.ListenerConfig{
		Config: base,
		SessionConfig: func(_ context.Context, _ *zmux.CarrierInfo, admitted any) (*zmux.Config, error) {
			return a.sessionConfig(base, admitted), nil
		},
		MaxSuspended:        maxSuspended,
		MaxPending:          maxPending,
		MaxPendingPerSource: maxPendingPerSource,
		Admission: &zmux.Admission{
			Peek: 1, MaxPeek: wire.MaxPeek, FallbackOnPeekTimeout: true, Decide: a.decide,
		},
		OnReject: onReject,
		Fallback: a.fallback,
		WrapPipe: &admitPipe{a: a, cfg: &seal.ServerConfig{
			Gate:            &gate{a: a},
			Key:             key,
			ClientAnchor:    acceptAnyClient{},
			AppData:         a.appData,
			MaxAppData:      wire.MaxHelloRecv,
			AllowRecordMode: allowEstablishMode,
		}},
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// admitPipe runs Establish, names the carrier on the ticket and reports the
// failure of a client the admission identified by its Handle.
type admitPipe struct {
	a   *Acceptor
	cfg *seal.ServerConfig
}

// Validate implements the check of zmux.NewListener.
func (w *admitPipe) Validate() error { return w.cfg.Validate() }

// WrapPipe implements zmux.PipeWrapper.
func (w *admitPipe) WrapPipe(ctx context.Context, p zmux.PipeIO, hint zmux.PipeHint) (zmux.PipeIO, error) {
	g := &gate{a: w.a}
	cfg := *w.cfg
	cfg.Gate, cfg.Shaper = g, g.handshake
	q, err := cfg.WrapPipe(ctx, p, hint)
	if err != nil {
		w.report(err, p, hint)
		return q, err
	}
	if peer, ok := seal.PeerOf(q); ok {
		if t, ok := peer.Value.(*ticket); ok {
			t.carrier, t.kind = carrierLabel(p), p.Carrier
		}
	}
	return q, nil
}

// carrierLabel names a carrier for the log; the HTTP and QUIC adapters give
// no label.
func carrierLabel(p zmux.PipeIO) string {
	switch _, ok := zmux.TerminatedTLS(p); {
	case p.Label != "":
		return p.Label
	case p.Carrier == zmux.CarrierQUIC:
		return "quic"
	case ok:
		return "wss"
	}
	return "ws"
}

func (w *admitPipe) report(err error, p zmux.PipeIO, hint zmux.PipeHint) {
	var re *zmux.RejectError
	e, known := hint.Admitted.(*clientEntry)
	if !errors.As(err, &re) || !known {
		return
	}
	// A failing Bound is how a TLS-terminating intermediary shows up
	// (§3.2): the client retries Sealed, and it is no attack.
	if re.Mode == zmux.Bound {
		return
	}
	var reason string
	switch re.Reason {
	case "auth":
		reason = "bad_key"
	case "malformed", "bad_flags", "trailing_bytes", "tlv_truncated", "tlv_non_minimal", "tlv_order", "tlv_unknown_critical", "too_large":
		reason = "malformed"
	default:
		return
	}
	w.a.authFailed(coreapi.AuthFailure{Source: hint.Source, Carrier: carrierLabel(p), Client: e.ID, Reason: reason})
}

func (a *Acceptor) appData(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
	if r.Message == 2 {
		return a.hello, nil
	}
	return nil, nil
}

// Start implements coreapi.Entry.
func (a *Acceptor) Start(ctx context.Context, h coreapi.Hooks) error {
	a.hooks.Store(&h)
	ctx, a.cancel = context.WithCancel(ctx)
	id := seal.NodeKeyID(a.key.PublicKey().Bytes())
	log.Info("core protocol entry ready", "key_id", hex.EncodeToString(id[:]))
	if a.shapingFrom != nil {
		a.wg.Go(func() { a.shapingFrom(ctx, a.SetShaping) })
	}
	a.wg.Go(func() {
		for {
			zc, err := a.zl.Accept(ctx)
			if err != nil {
				return
			}
			a.wg.Go(func() { a.handle(ctx, zc) })
		}
	})
	return nil
}

// Close implements coreapi.Entry.
func (a *Acceptor) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	_ = a.zl.Close()
	a.mu.Lock()
	var all []*serverSession
	for _, l := range a.sess {
		all = append(all, l...)
	}
	a.mu.Unlock()
	for _, s := range all {
		_ = s.zc.Close()
	}
	a.wg.Wait()
	return nil
}

// Serve implements coreapi.Entry.
func (a *Acceptor) Serve(ctx context.Context, c net.Conn, carrier string) {
	a.serve(ctx, c, carrierCtx{label: carrier})
}

// ServeMixed implements coreapi.Entry.
func (a *Acceptor) ServeMixed(ctx context.Context, c net.Conn, carrier string, other func(net.Conn, *tls.ConnectionState) bool) {
	a.serve(ctx, c, carrierCtx{label: carrier, other: other})
}

// QUICConfig implements coreapi.Entry.
func (a *Acceptor) QUICConfig(base *quic.Config) *quic.Config { return zmuxquic.QUICConfig(base) }

// ServeQUIC implements coreapi.Entry. ALPN told the new protocol from the old
// before this (§12), so a first stream that is no carrier just ends.
func (a *Acceptor) ServeQUIC(ctx context.Context, qc *quic.Conn) {
	_ = zmuxquic.ServeConn(ctx, qc, a.zl, func(_ context.Context, _ *quic.Conn, p zmux.PipeIO) {
		_ = zmux.PipeConn(p).Close()
	})
}

func (a *Acceptor) serve(ctx context.Context, c net.Conn, info carrierCtx) {
	var p zmux.PipeIO
	carrier := info.label
	if tc, ok := c.(*tls.Conn); ok {
		// A connection this process accepted itself: end to end.
		p, info.tls = zmux.SecureTLS(tc), tc
	} else {
		p = zmux.NetPipe(c)
	}
	p.Label = carrier
	ctx = context.WithValue(ctx, carrierKey{}, info)
	_ = a.zl.HandlePipe(ctx, p, zmux.PipeHint{Source: addrPort(c.RemoteAddr())})
}

// WebSocket implements coreapi.Entry (§12): on the bridge path the subprotocol
// binary is the core protocol; anything else stays with the old one.
func (a *Acceptor) WebSocket(legacy http.Handler, f coreapi.Forwarding) (http.Handler, error) {
	return zmuxws.Handler(a.zl, &zmux.HTTPOptions{Next: legacy, Trusted: trustedOf(f)}, legacySubprotocol)
}

// forwarding is the coreapi.Forwarding of zmux's resolver, so that the route
// and the admission agree on a request's source.
type forwarding struct{ t *zmux.TrustedProxies }

func (f forwarding) Source(r *http.Request) netip.AddrPort {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.AddrPort{}
	}
	return f.t.Source(r, peer)
}

func trustedOf(f coreapi.Forwarding) *zmux.TrustedProxies {
	if v, ok := f.(forwarding); ok {
		return v.t
	}
	return nil
}

// Forwarding implements coreapi.Entry.
func (a *Acceptor) Forwarding(trusted []netip.Prefix, header string) coreapi.Forwarding {
	if len(trusted) == 0 {
		return nil
	}
	return forwarding{&zmux.TrustedProxies{Prefixes: trusted, Header: header}}
}

// WebSocketAnyProtocol implements coreapi.Entry (§12): the path is the core
// protocol's, so a request is accepted with the subprotocol it offered (the
// first of them) or with none.
func (a *Acceptor) WebSocketAnyProtocol(next http.Handler, f coreapi.Forwarding) (http.Handler, error) {
	o := &zmux.HTTPOptions{Next: next, Trusted: trustedOf(f)}
	if _, err := zmuxws.Handler(a.zl, o); err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocols := []string{zmuxws.NoSubprotocol}
		for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
			for p := range strings.SplitSeq(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					protocols = append(protocols, p)
				}
			}
		}
		// Built per request: it holds no state, and the protocols are the
		// request's own, which is what accepting any subprotocol means.
		h, err := zmuxws.Handler(a.zl, o, protocols...)
		if err != nil {
			o.Fallback(w, r)
			return
		}
		h.ServeHTTP(w, r)
	}), nil
}

// decide routes a carrier by its first bytes (§12, §13). It is the one place
// that does, for every entry point of the listener.
func (a *Acceptor) decide(_ context.Context, info *zmux.CarrierInfo, prefix []byte) zmux.Decision {
	if info.Head {
		// The HTTP handler matched the path and subprotocol.
		return zmux.Decision{Verdict: zmux.VerdictAccept}
	}
	if len(prefix) == 0 {
		return zmux.Decision{Verdict: zmux.VerdictFallback}
	}
	switch b := prefix[0]; b {
	case 2:
		known, need := a.zl.KnownJoin(prefix)
		if need > 0 {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: need}
		}
		if known {
			return zmux.Decision{Verdict: zmux.VerdictAccept}
		}
	case 1, 3:
		if len(prefix) < wire.HelloHeadLen {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: wire.HelloHeadLen}
		}
		if e := a.table.lookup(prefix[2:wire.HelloHeadLen]); e != nil {
			return zmux.Decision{Verdict: zmux.VerdictAccept, Value: e}
		}
		log.Debug("unknown handle", "src_ip", info.Source.Addr().Unmap().String())
	case legacyMagic[0]:
		if len(prefix) < len(legacyMagic) {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: len(legacyMagic)}
		}
	}
	return zmux.Decision{Verdict: zmux.VerdictFallback}
}

// fallback takes what the admission did not: TST goes to the old protocol,
// anything else to the domain proxy, or stalls when there is none. It owns c.
func (a *Acceptor) fallback(ctx context.Context, c net.Conn) {
	head := make([]byte, len(legacyMagic))
	// The prefix is replayed at once; the deadline matters only for a
	// carrier that took the peek timeout with fewer bytes.
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := io.ReadFull(c, head)
	_ = c.SetReadDeadline(time.Time{})
	nc := zmux.PrefixedConn(c, head[:n])
	info, _ := ctx.Value(carrierKey{}).(carrierCtx)
	if info.other != nil && info.other(nc, tlsState(info)) {
		return
	}
	h := a.hooks.Load()
	if info.other == nil && h != nil && n == len(head) && string(head) == legacyMagic && h.Legacy != nil {
		h.Legacy(nc, info.label)
		return
	}
	if info.other == nil && h != nil && h.Fallback != nil && h.Fallback(nc, tlsState(info)) {
		return
	}
	a.Stall(ctx, nc)
}

// Stall implements coreapi.Entry.
func (a *Acceptor) Stall(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	src := addrPort(c.RemoteAddr())
	key := stallKey(src)
	if !a.acquireStall(key) {
		return
	}
	defer a.releaseStall(key)
	t := time.NewTimer(a.zl.StallDuration(src))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// stallKey aggregates a source as the admission does: an IPv6 address to its
// /64.
func stallKey(src netip.AddrPort) netip.Prefix {
	addr := src.Addr().Unmap()
	if !addr.IsValid() {
		return netip.Prefix{}
	}
	bits := addr.BitLen()
	if addr.Is6() {
		bits = 64
	}
	p, _ := addr.Prefix(bits)
	return p
}

func (a *Acceptor) acquireStall(key netip.Prefix) bool {
	a.stallMu.Lock()
	defer a.stallMu.Unlock()
	if a.stallTotal >= a.stallMax || a.stalled[key] >= a.stallPerSource {
		return false
	}
	a.stallTotal++
	a.stalled[key]++
	return true
}

func (a *Acceptor) releaseStall(key netip.Prefix) {
	a.stallMu.Lock()
	defer a.stallMu.Unlock()
	a.stallTotal--
	if a.stalled[key]--; a.stalled[key] == 0 {
		delete(a.stalled, key)
	}
}

// tlsState is the TLS of a carrier this process terminated, nil for none.
func tlsState(info carrierCtx) *tls.ConnectionState {
	if info.tls == nil {
		return nil
	}
	st := info.tls.ConnectionState()
	return &st
}

func addrPort(a net.Addr) netip.AddrPort {
	if a == nil {
		return netip.AddrPort{}
	}
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}

func (a *Acceptor) authFailed(f coreapi.AuthFailure) {
	if h := a.hooks.Load(); h != nil && h.AuthFailed != nil {
		h.AuthFailed(f)
	}
}

// handle runs an established session until it ends.
func (a *Acceptor) handle(ctx context.Context, zc *zmux.Conn) {
	peer, ok := zc.Admitted().(*seal.Peer)
	if !ok {
		_ = zc.Close()
		return
	}
	t, ok := peer.Value.(*ticket)
	if !ok {
		_ = zc.Close()
		return
	}
	s := &serverSession{a: a, zc: zc, t: t, mode: peer.Mode, since: time.Now(), kicked: make(chan struct{}, 1)}
	if e := a.table.get(t.client); e != nil {
		s.configOnly = e.ConfigOnly
		zc.SetScheduler(schedulerNamed(e.SchedulerDown))
	}
	if a.sessionOpts.IdleS != 0 || a.sessionOpts.DeadS != 0 {
		if err := zc.SetLiveness(livenessOf(a.sessionOpts)); err != nil {
			log.Debug("session options not applied", "err", err)
		}
	}
	replaced := a.register(s)
	a.dropGrant(t)
	h := a.hooks.Load()
	for _, old := range replaced {
		old.closing.Store("replaced")
		if h != nil && h.Takeover != nil {
			h.Takeover(s, old)
		}
		a.drain(old)
	}
	if h != nil && h.Online != nil && !s.configOnly {
		h.Online(s)
	}
	// The client may have changed between Commit and register.
	if e := a.table.get(t.client); e == nil || !e.Enabled || e.handle != t.handle {
		s.closeAuth("deleted")
	} else if e.Crypt && s.mode == zmux.Trusted {
		a.drain(s)
	}
	watched := make(chan struct{})
	a.wg.Go(func() {
		defer close(watched)
		s.watch(ctx, h)
	})
	a.wg.Go(func() { rejectUniStreams(ctx, zc, wire.Op.FromClient) })
	a.wg.Go(func() { s.serveStreams(ctx) })
	a.wg.Go(func() { s.pushLoop(ctx) })
	s.kick()
	_ = zc.Wait()
	// The last SuspendChanged has run: Offline is the end of the session's
	// hooks.
	<-watched
	a.unregister(s)
	if h != nil && h.Offline != nil && !s.configOnly {
		reason, _ := s.closing.Load().(string)
		if reason == "" {
			reason = "closed"
		}
		h.Offline(s, reason)
	}
}

// snapshotOf is a client's CONFIG: the record rule of §3.2, its default
// compression, the session options and the health checks. The session's own
// shaping follows what it carries.
func (a *Acceptor) snapshotOf(s *serverSession) *wire.Snapshot {
	snap := &wire.Snapshot{Session: a.sessionOpts}
	if s.CanHealth() {
		snap.Health = s.healthGroups()
	}
	if e := a.table.get(s.t.client); e != nil {
		if !e.Crypt {
			snap.Record = wire.RecordTrusted
		}
		snap.Compress = chooseCompress(e.Compress, s.t.hello.CompressAlgs)
		if i := slices.Index(connprofile.Schedulers, e.SchedulerUp); i >= 0 && e.SchedulerUp != "" {
			snap.SchedulerUp, snap.HasSchedulerUp = uint64(i), true
		}
		p := a.shapingFor(s.t, e.Shaping)
		if p != nil {
			snap.Shaping = p.raw
		}
		s.follow(p)
	}
	return snap
}

// register adds s and returns the sessions of the same instance it replaces
// (§3.6); sessions of other instances are left alone.
func (a *Acceptor) register(s *serverSession) (replaced []*serverSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.sess[s.t.client][:0:0]
	for _, o := range a.sess[s.t.client] {
		if bytes.Equal(o.t.static, s.t.static) {
			replaced = append(replaced, o)
		} else {
			kept = append(kept, o)
		}
	}
	a.sess[s.t.client] = append(kept, s)
	return replaced
}

func (a *Acceptor) unregister(s *serverSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.sess[s.t.client]
	for i, o := range l {
		if o == s {
			l = append(l[:i:i], l[i+1:]...)
			break
		}
	}
	if len(l) == 0 {
		delete(a.sess, s.t.client)
	} else {
		a.sess[s.t.client] = l
	}
}

func (a *Acceptor) sessionsOf(id int) []*serverSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*serverSession(nil), a.sess[id]...)
}

// drain ends s with NPS_DRAIN in the background. A suspended session has no
// pipe to say it on, and the takeover or the kick that drains it must not wait
// for its grace period.
func (a *Acceptor) drain(s *serverSession) {
	a.wg.Go(func() {
		if s.Suspended() {
			_ = s.zc.CloseWithError(zmux.ApplicationErrorCode(wire.CodeDrain), "")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		_ = s.zc.Shutdown(ctx, zmux.ApplicationErrorCode(wire.CodeDrain), "")
	})
}

// ClientChanged implements coreapi.Entry: §3.2's rules for the switch, §3.7
// for a client that may no longer connect; a change of what CONFIG carries
// goes out as a new snapshot.
func (a *Acceptor) ClientChanged(r coreapi.ClientRecord) {
	old := a.table.set(r)
	sessions := a.sessionsOf(r.ID)
	for _, s := range sessions {
		s.zc.SetScheduler(schedulerNamed(r.SchedulerDown))
	}
	switch {
	case !r.Enabled || old != nil && old.VKey != r.VKey:
		for _, s := range sessions {
			s.closeAuth("deleted")
		}
		a.revokeClientLinks(r.ID)
	case old == nil:
		for _, s := range sessions {
			s.kick()
		}
	case !old.Crypt && r.Crypt:
		for _, s := range sessions {
			a.drain(s)
		}
	case old.Crypt != r.Crypt || old.Shaping != r.Shaping || old.SchedulerUp != r.SchedulerUp || !slices.Equal(old.Compress, r.Compress):
		for _, s := range sessions {
			s.kick()
		}
	}
}

// ClientRemoved implements coreapi.Entry.
func (a *Acceptor) ClientRemoved(id int) {
	a.table.remove(id)
	for _, s := range a.sessionsOf(id) {
		s.closeAuth("deleted")
	}
	a.revokeClientLinks(id)
}

// revokeClientLinks ends the links of a client whose credential no longer
// holds. A client that is merely offline keeps them (§9.1).
func (a *Acceptor) revokeClientLinks(id int) {
	a.RevokeLinks(func(_, visitor, provider int) bool { return visitor == id || provider == id })
}

// Kick implements coreapi.Entry.
func (a *Acceptor) Kick(id int, auth bool) {
	for _, s := range a.sessionsOf(id) {
		if auth {
			s.closeAuth("deleted")
		} else {
			s.closing.CompareAndSwap(nil, "kicked")
			a.drain(s)
		}
	}
	if auth {
		a.revokeClientLinks(id)
	}
}

// serverSession implements coreapi.Session.
type serverSession struct {
	a       *Acceptor
	zc      *zmux.Conn
	t       *ticket
	mode    zmux.RecordMode
	since   time.Time
	seq     atomic.Uint64
	kicked  chan struct{} // a newer snapshot wants pushing
	closing atomic.Value  // string: reason of a close this side made
	// suspended is true between EventSessionSuspended and
	// EventSessionResumed; only watch writes it.
	suspended atomic.Bool
	// configOnly is a session of the public key.
	configOnly bool

	healthMu sync.Mutex
	health   []wire.HealthGroup

	serviceMu  sync.Mutex
	serviceSeq uint64 // of the applied SERVICE snapshot
}

func (s *serverSession) ClientID() int          { return s.t.client }
func (s *serverSession) Instance() string       { return string(s.t.static) }
func (s *serverSession) Source() netip.AddrPort { return s.t.src }
func (s *serverSession) Carrier() string        { return s.t.carrier }
func (s *serverSession) Agent() string          { return s.t.hello.Agent }
func (s *serverSession) Since() time.Time       { return s.since }

// Close implements coreapi.Session.
func (s *serverSession) Close() error {
	s.closing.CompareAndSwap(nil, "kicked")
	return s.zc.CloseWithError(zmux.ApplicationErrorCode(wire.CodeDrain), "")
}

func (s *serverSession) closeAuth(reason string) {
	s.closing.Store(reason)
	_ = s.zc.CloseWithError(zmux.ApplicationErrorCode(wire.CodeAuth), "")
}

// Suspended implements coreapi.Session.
func (s *serverSession) Suspended() bool { return s.suspended.Load() && !s.Closed() }

// watch follows the state of the session from its events until it ends.
func (s *serverSession) watch(ctx context.Context, h *coreapi.Hooks) {
	var since time.Time
	for {
		ev, err := s.zc.NextEvent(ctx)
		if err != nil {
			return
		}
		switch ev.Kind {
		case zmux.EventSessionSuspended:
			since = time.Now()
			s.suspended.Store(true)
			slogx.Emit(ctx, log, &logevent.SessionSuspended, s.logFields()...)
		case zmux.EventSessionResumed:
			s.suspended.Store(false)
			slogx.Emit(ctx, log, &logevent.SessionResumed, append(s.logFields(), "dur_ms", time.Since(since).Milliseconds())...)
		default:
			continue
		}
		if h != nil && h.SuspendChanged != nil {
			h.SuspendChanged(s)
		}
	}
}

func (s *serverSession) logFields() []any {
	return []any{"client", s.t.client, "src_ip", s.t.src.Addr().Unmap().String(), "carrier", s.t.carrier}
}

// kick asks for a new snapshot; concurrent changes coalesce into one push of
// the latest state.
func (s *serverSession) kick() {
	select {
	case s.kicked <- struct{}{}:
	default:
	}
}

// configWriteTimeout bounds pushing one snapshot to a peer that stopped
// reading.
const configWriteTimeout = 30 * time.Second

// pushLoop sends snapshots, one at a time, so that seq order is the order of
// their content (§4.3).
func (s *serverSession) pushLoop(ctx context.Context) {
	stop := s.zc.Context().Done()
	for {
		select {
		case <-s.kicked:
			if err := s.pushConfig(s.a.snapshotOf(s)); err != nil {
				log.Debug("config push failed", "client", s.t.client, "err", err)
			}
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// pushConfig opens one stream carrying a full snapshot (§4.3).
func (s *serverSession) pushConfig(snap *wire.Snapshot) error {
	st, err := s.zc.OpenUniStream()
	if err != nil {
		return err
	}
	_ = st.SetWriteDeadline(time.Now().Add(configWriteTimeout))
	if err := st.SetPreamble(wire.AppendConfigOpen(s.seq.Add(1))); err != nil {
		st.CancelWrite(zmux.StreamErrorCode(wire.CodeInternal))
		return err
	}
	if body := wire.AppendSnapshot(nil, snap); len(body) > 0 {
		if _, err := st.Write(body); err != nil {
			st.CancelWrite(zmux.StreamErrorCode(wire.CodeInternal))
			return err
		}
	}
	return st.Close()
}
