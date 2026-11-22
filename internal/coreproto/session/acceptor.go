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

	"github.com/djylb/lifecycle"
	"github.com/djylb/netx"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxhttp"
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
	maxPending = 4096
	// Unauthenticated carriers count per source prefix: one network does not
	// get round the limit by taking another address of its own. A prefix
	// may hold a carrier-grade NAT of many clients, hence the room.
	maxPendingPerSource = 256
	sourcePrefixV4      = 24
	sourcePrefixV6      = 56
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

	usageMu sync.Mutex
	usage   map[uint64]*linkUsage // direct usage reported per link (§9.6)
	clock   func() time.Time      // of the usage records; called with usageMu held

	mu   sync.Mutex
	sess map[int][]*serverSession
	// closing is set by Close under mu: a session registered after its
	// drain snapshot is drained at once.
	closing bool
	grants  map[int][]*pipeGrant // client_max_pipes: pipes admitted, not yet counted

	stallMu                  sync.Mutex
	stalled                  map[netip.Prefix]int
	stallTotal               int
	stallMax, stallPerSource int

	admitKey []byte
	exchange struct {
		once sync.Once
		h    *zmuxhttp.ExchangeHandler
		err  error
	}

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
	// AdmitKey is the PSK of the exchange carrier (§13); nil offers none.
	AdmitKey []byte
}

// NewAcceptor builds the entry over store, with a key of this process
// (nothing is persisted, §3.3).
func NewAcceptor(store Store, opts Options) (*Acceptor, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	a := &Acceptor{links: map[uint64]*peerLink{}, usage: map[uint64]*linkUsage{}, clock: time.Now, table: newHandleTable(store), key: key, hello: serverHello(), sess: map[int][]*serverSession{},
		grants: map[int][]*pipeGrant{}, sessionOpts: opts.Session, limits: opts.Compress.normalized(), maxPipes: opts.MaxPipes, shapingFrom: opts.ShapingSource,
		admitKey: opts.AdmitKey, stalled: map[netip.Prefix]int{}, stallMax: maxStalled, stallPerSource: maxStalledPerSource}
	base := boundBuffers(&zmux.Config{AllowRecordMode: a.allowJoinMode, AuthorizeJoin: a.authorizeJoin, MaxLanes: maxLanesOf(opts.MaxLanes),
		EnableDatagrams: true, MaxPipes: sessionMaxPipes, ResumeTimeout: resumeTimeoutOf(opts.ResumeTimeout)})
	a.zl, err = zmux.NewListener(&zmux.ListenerConfig{
		Config: base,
		SessionConfig: func(_ context.Context, _ *zmux.CarrierInfo, admitted any) (*zmux.Config, error) {
			return a.sessionConfig(base, admitted), nil
		},
		MaxSuspended:        maxSuspended,
		MaxPending:          maxPending,
		MaxPendingPerSource: maxPendingPerSource,
		SourcePrefixV4:      sourcePrefixV4,
		SourcePrefixV6:      sourcePrefixV6,
		Admission: &zmux.Admission{
			Peek: 1, MaxPeek: wire.MaxPeek, FallbackOnPeekTimeout: true, Decide: a.decide,
		},
		OnReject: onReject,
		Fallback: a.fallback,
		WrapPipe: &admitPipe{a: a, cfg: &seal.ServerConfig{
			Gate:            &gate{a: a},
			StaticKey:       key,
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
			t.carrier, t.kind = shortLabel(ctx, p), p.Carrier
		}
	}
	// zmux reports the variant of every pipe in its statistics, and nothing
	// else of a joined pipe says what carries it (serverSession.Pipes).
	if q.Variant == "" {
		q.Variant = httpLabel(ctx, p)
	}
	return q, nil
}

// carrierLabel names a carrier for the log; the HTTP and QUIC adapters give
// no label.
func carrierLabel(p zmux.PipeIO) string {
	_, terminated := zmux.TerminatedTLS(p)
	return kindLabelTLS(p.Label, p.Carrier, terminated)
}

// kindLabel is carrierLabel of a pipe already joined, whose TLS is not known.
func kindLabel(label string, kind zmux.CarrierKind) string {
	return kindLabelTLS(label, kind, false)
}

func kindLabelTLS(label string, kind zmux.CarrierKind, tls bool) string {
	switch {
	case label != "":
		return label
	case kind == zmux.CarrierQUIC:
		return "quic"
	case kind == zmux.CarrierHTTP3:
		return "h3"
	case kind == zmux.CarrierStream, kind == zmux.CarrierSplit, kind == zmux.CarrierExchange:
		return "http"
	case tls:
		return "wss"
	}
	return "ws"
}

// httpKey holds the request an HTTP carrier came in by (withRequest).
type httpKey struct{}

// httpReq is what the label of an HTTP carrier shows of its request.
type httpReq struct {
	tls   bool
	major int
}

// withRequest lets the label of a carrier of h name the scheme and HTTP
// version of its request: several kinds and versions share one entry.
func withRequest(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpKey{}, httpReq{tls: r.TLS != nil, major: r.ProtoMajor})))
	})
}

// httpLabel is carrierLabel, naming for a carrier of an HTTP request its
// scheme, HTTP version and kind: "https h2 streaming", "wss".
func httpLabel(ctx context.Context, p zmux.PipeIO) string {
	r, ok := ctx.Value(httpKey{}).(httpReq)
	kind, known := httpKinds[p.Carrier]
	if !ok || !known || p.Label != "" {
		return carrierLabel(p)
	}
	version := "http/1.1"
	switch r.major {
	case 2:
		version = "h2"
	case 3:
		version = "h3"
	}
	if p.Carrier == zmux.CarrierWebSocket {
		// "ws", "wss", "wss h2": WebSocket is told by its scheme.
		scheme := "ws"
		if r.tls {
			scheme = "wss"
		}
		if r.major == 1 {
			return scheme
		}
		return scheme + " " + version
	}
	scheme := "http"
	if r.tls {
		scheme = "https"
	}
	return scheme + " " + version + " " + kind
}

// shortLabel names the carrier a session started on, for lists and logs:
// the scheme of an HTTP carrier ("https", "wss", "h3") without the version
// and kind its pipe statistics give.
func shortLabel(ctx context.Context, p zmux.PipeIO) string {
	r, ok := ctx.Value(httpKey{}).(httpReq)
	if _, known := httpKinds[p.Carrier]; !ok || !known || p.Label != "" {
		return carrierLabel(p)
	}
	switch {
	case p.Carrier == zmux.CarrierHTTP3:
		return "h3"
	case p.Carrier == zmux.CarrierWebSocket && r.tls:
		return "wss"
	case p.Carrier == zmux.CarrierWebSocket:
		return "ws"
	case r.tls:
		return "https"
	}
	return "http"
}

var httpKinds = map[zmux.CarrierKind]string{zmux.CarrierWebSocket: "", zmux.CarrierStream: "streaming",
	zmux.CarrierSplit: "split", zmux.CarrierExchange: "exchange", zmux.CarrierHTTP3: "streaming"}

func (w *admitPipe) report(err error, p zmux.PipeIO, hint zmux.PipeHint) {
	var re *zmux.RejectError
	e, known := hint.Admitted.(*clientEntry)
	if !errors.As(err, &re) || !known {
		return
	}
	// A failing Bound is how a TLS-terminating intermediary shows up
	// (§3.2): the client retries Sealed, and it is no attack.
	if re.RecordMode == zmux.RecordBound {
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
	id := seal.StaticKeyID(a.key.PublicKey().Bytes())
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
	// Sessions end with NPS_DRAIN while their carriers still run, so npc
	// redials at once instead of waiting out its resume timeout.
	a.mu.Lock()
	a.closing = true
	var all []*serverSession
	for _, l := range a.sess {
		all = append(all, l...)
	}
	a.mu.Unlock()
	for _, s := range all {
		_ = s.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	_ = a.zl.Close()
	a.exchange.once.Do(func() {}) // none is built past this point
	if a.exchange.h != nil {
		_ = a.exchange.h.Close()
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

// ServeTerminated implements coreapi.Entry.
func (a *Acceptor) ServeTerminated(ctx context.Context, tc *tls.Conn, head []byte, carrier string) {
	p := zmux.PrefixedPipe(zmux.SecureTLS(tc), head)
	p.Label = carrier
	ctx = context.WithValue(ctx, carrierKey{}, carrierCtx{label: carrier, tls: tc})
	_ = a.zl.HandlePipe(ctx, p, zmux.PipeHint{Source: addrPort(tc.RemoteAddr())})
}

// QUICConfig implements coreapi.Entry.
func (a *Acceptor) QUICConfig(base *quic.Config) *quic.Config { return zmuxquic.QUICConfig(base) }

// ServeQUIC implements coreapi.Entry. ALPN told the new protocol from the old
// before this (§12), so a first stream that is no carrier just ends.
// It returns when the connection ends: its caller closes qc after.
func (a *Acceptor) ServeQUIC(ctx context.Context, qc *quic.Conn) {
	if zmuxquic.ServeConn(ctx, a.zl, qc) == nil {
		<-qc.Context().Done()
	}
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
	h, err := zmuxws.NewHandler(a.zl, &zmuxhttp.Options{Next: legacy, Trusted: trustedOf(f)}, legacySubprotocol)
	if err != nil {
		return nil, err
	}
	return withRequest(h), nil
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
	o := &zmuxhttp.Options{Next: next, Trusted: trustedOf(f)}
	if _, err := zmuxws.NewHandler(a.zl, o); err != nil {
		return nil, err
	}
	return withRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		h, err := zmuxws.NewHandler(a.zl, o, protocols...)
		if err != nil {
			o.Fallback(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})), nil
}

// decide routes a carrier by its first bytes (§12, §13). It is the one place
// that does, for every entry point of the listener.
func (a *Acceptor) decide(_ context.Context, info zmux.CarrierInfo, prefix []byte) zmux.Decision {
	if info.Stage == zmux.StageHead {
		// The HTTP handler matched the path and subprotocol.
		return zmux.Decision{Verdict: zmux.VerdictAccept}
	}
	if len(prefix) == 0 {
		return zmux.Decision{Verdict: zmux.VerdictFallback}
	}
	switch b := prefix[0]; b {
	case 2:
		known, need := a.zl.Adapter().KnownJoin(prefix)
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
		// A key no client has: a wrong key, told apart from no other.
		a.authFailed(coreapi.AuthFailure{Source: info.Source, Reason: "bad_key"})
	case legacyMagic[0]:
		if len(prefix) < len(legacyMagic) {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: len(legacyMagic)}
		}
	}
	return zmux.Decision{Verdict: zmux.VerdictFallback}
}

// fallback takes what the admission did not: TST goes to the old protocol,
// anything else to the domain proxy, or stalls when there is none. It owns c.
func (a *Acceptor) fallback(ctx context.Context, t *zmux.Takeover) {
	if _, ok := zmuxquic.TakeoverConn(t); ok {
		// ALPN told the protocols apart: a QUIC stream that is no carrier
		// just ends, closed by zmux on return.
		return
	}
	// The routes below own the carrier and may serve it after return.
	if t.Detach() != nil {
		return
	}
	c, ok := t.Conn()
	if !ok {
		c = zmux.PipeConn(t.Pipe())
	}
	head := make([]byte, len(legacyMagic))
	// The prefix is replayed at once; the deadline matters only for a
	// carrier that took the peek timeout with fewer bytes.
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := io.ReadFull(c, head)
	_ = c.SetReadDeadline(time.Time{})
	nc := c
	if n > 0 {
		// nc wraps c, which every path below hands on to its owner.
		//goland:noinspection GoResourceLeak
		nc = netx.NewPrefixConn(c, head[:n])
	}
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
	t := time.NewTimer(a.zl.Adapter().StallDuration(src))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// stallKey aggregates a source as the admission does, to its source prefix.
func stallKey(src netip.AddrPort) netip.Prefix {
	addr := src.Addr().Unmap()
	if !addr.IsValid() {
		return netip.Prefix{}
	}
	bits := sourcePrefixV4
	if addr.Is6() {
		bits = sourcePrefixV6
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
	peer, ok := zmux.AdmittedAs[*seal.Peer](zc)
	if !ok {
		_ = zc.Close()
		return
	}
	t, ok := peer.Value.(*ticket)
	if !ok {
		_ = zc.Close()
		return
	}
	s := &serverSession{a: a, zc: zc, t: t, mode: peer.RecordMode, since: time.Now(), ctx: ctx, hooks: a.hooks.Load(), scope: newSessionScope()}
	if e := a.table.get(t.client); e != nil {
		s.configOnly = e.ConfigOnly
		s.visitorKey = e.VisitorOnly
		zc.SetScheduler(schedulerNamed(e.SchedulerDown))
	}
	s.visitorOnly = !s.configOnly && (s.visitorKey || t.hello.VisitorOnly)
	if a.sessionOpts.IdleS != 0 || a.sessionOpts.DeadS != 0 {
		if err := zc.SetLiveness(livenessOf(a.sessionOpts)); err != nil {
			log.Debug("session options not applied", "err", err)
		}
	}
	replaced, closing := a.register(s)
	a.dropGrant(t)
	h := a.hooks.Load()
	for _, old := range replaced {
		old.closing.Store("replaced")
		if h != nil && h.Takeover != nil && s.node() {
			h.Takeover(s, old)
		}
		a.drain(old)
	}
	if h != nil && h.Online != nil && s.node() {
		h.Online(s)
	}
	// The client may have changed between Commit and register.
	if e := a.table.get(t.client); e == nil || !e.Enabled || e.handle != t.handle {
		s.closeAuth("deleted")
	} else if closing || e.Crypt && s.mode == zmux.RecordTrusted {
		a.drain(s)
	}
	// The session runs on zmux callbacks, each a task of s.scope: no
	// goroutine stays per session while nothing happens on it.
	if err := zc.HandleEvents(s.event); err != nil {
		log.Debug("session events not watched", "client", t.client, "err", err)
	}
	if err := rejectUniStreams(zc, wire.Op.FromClient); err != nil {
		log.Debug("session uni streams not served", "client", t.client, "err", err)
	}
	if err := handleStreams(zc, s.scope, func(st *zmux.Stream) { s.serveStream(ctx, st) }); err != nil {
		log.Debug("session streams not served", "client", t.client, "err", err)
	}
	s.kick()
	_ = zc.Wait()
	// No callback starts past Drain; the scope ends with the last running
	// one, the last SuspendChanged among them: Offline is the end of the
	// session's hooks. zmux's Wait waits for none of them, and they may
	// still use the session, so they end before its cleanup.
	s.scope.Drain()
	_ = s.scope.Wait(context.Background())
	a.unregister(s)
	if h != nil && h.Offline != nil && s.node() {
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

// register adds s and returns the sessions of the same instance and role it
// replaces (§3.6); sessions of other instances are left alone, and so is the
// client's own session when a pure visitor of the same process connects.
func (a *Acceptor) register(s *serverSession) (replaced []*serverSession, closing bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.sess[s.t.client][:0:0]
	for _, o := range a.sess[s.t.client] {
		if bytes.Equal(o.t.static, s.t.static) && o.visitorOnly == s.visitorOnly {
			replaced = append(replaced, o)
		} else {
			kept = append(kept, o)
		}
	}
	a.sess[s.t.client] = append(kept, s)
	return replaced, a.closing
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
	since        time.Time
	suspendSince time.Time // of the last EventSessionSuspended; only event uses it
	ctx          context.Context
	closing      atomic.Value // string: reason of a close this side made
	t            *ticket
	hooks        *coreapi.Hooks
	// scope holds the session's zmux callbacks and its pusher as tasks;
	// handle drains it when the session ended and waits for it before the
	// session's cleanup.
	scope  *lifecycle.Scope
	a      *Acceptor
	zc     *zmux.Conn
	health []wire.HealthGroup // under healthMu

	seq        atomic.Uint64
	serviceSeq uint64 // of the applied SERVICE snapshot; under serviceMu

	healthMu  sync.Mutex
	serviceMu sync.Mutex

	// dirty: a newer snapshot wants pushing; pushing: a pusher runs (kick).
	pushing atomic.Bool
	dirty   atomic.Bool
	// suspended is true between EventSessionSuspended and
	// EventSessionResumed; only event writes it.
	suspended atomic.Bool
	// visitorKey is a session of the visitor key: visitorOnly whatever its
	// Hello says, and it uploads or registers nothing.
	visitorKey bool
	mode       zmux.RecordMode
	// visitorOnly is a session that only visits (wire.Hello.VisitorOnly),
	// whatever its key: it makes links and is never a provider or a node of
	// its client, so it does not displace the client's own session.
	visitorOnly bool
	// configOnly is a session of the public key.
	configOnly bool
}

// node reports whether the session is a node of its client: online, offline
// and takeover concern it.
func (s *serverSession) node() bool { return !s.configOnly && !s.visitorOnly }

func (s *serverSession) ClientID() int          { return s.t.client }
func (s *serverSession) Instance() string       { return string(s.t.static) }
func (s *serverSession) Source() netip.AddrPort { return s.t.src }
func (s *serverSession) Carrier() string        { return s.t.carrier }
func (s *serverSession) Agent() string          { return s.t.hello.Agent }
func (s *serverSession) LocalAddr() string      { return s.t.hello.LocalAddr }
func (s *serverSession) Record() string         { return s.mode.String() }
func (s *serverSession) Since() time.Time       { return s.since }

// Pipes implements coreapi.Session: the pipes that carry data, in pipe
// order.
func (s *serverSession) Pipes() []coreapi.PipeInfo {
	stats := s.zc.PipeStats()
	out := make([]coreapi.PipeInfo, 0, len(stats))
	for _, p := range stats {
		if usable(p) {
			out = append(out, coreapi.PipeInfo{Carrier: kindLabel(p.Variant, p.Carrier), RTT: p.SmoothedRTT,
				Attached: p.AttachedFor, Sent: p.BytesSent, Received: p.BytesReceived, Rate: p.DeliveryRate, Standby: p.Standby})
		}
	}
	return out
}

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

// event follows the state of the session (zmux.Conn.HandleEvents: one at a
// time). An event that comes once the session's callbacks drain is dropped:
// the session ended, and Offline follows.
func (s *serverSession) event(ev zmux.Event) {
	task, err := s.scope.Enter()
	if err != nil {
		return
	}
	defer task.End()
	switch ev.Kind {
	case zmux.EventSessionSuspended:
		s.suspendSince = time.Now()
		s.suspended.Store(true)
		slogx.Emit(s.ctx, log, &logevent.SessionSuspended, s.logFields()...)
	case zmux.EventSessionResumed:
		s.suspended.Store(false)
		slogx.Emit(s.ctx, log, &logevent.SessionResumed, append(s.logFields(), "dur_ms", time.Since(s.suspendSince).Milliseconds())...)
	default:
		return
	}
	if h := s.hooks; h != nil && h.SuspendChanged != nil {
		h.SuspendChanged(s)
	}
}

func (s *serverSession) logFields() []any {
	return []any{"client", s.t.client, "src_ip", s.t.src.Addr().Unmap().String(), "carrier", s.t.carrier}
}

// kick asks for a new snapshot; concurrent changes coalesce into one push of
// the latest state. A pusher runs only while there is something to push, at
// most one per session, so that seq order is the order of their content
// (§4.3).
func (s *serverSession) kick() {
	s.dirty.Store(true)
	if !s.pushing.CompareAndSwap(false, true) {
		return // the running pusher sees dirty
	}
	task, err := s.scope.Enter()
	if err != nil {
		// The session ended: nothing is pushed any more.
		s.pushing.Store(false)
		return
	}
	go s.push(&task)
}

// push sends snapshots until none is wanted; it owns task and ends it.
func (s *serverSession) push(task *lifecycle.Task) {
	defer task.End()
	for {
		for s.dirty.Swap(false) {
			if s.zc.Context().Err() != nil || s.ctx.Err() != nil {
				s.pushing.Store(false)
				return
			}
			if err := s.pushConfig(s.ctx, s.a.snapshotOf(s)); err != nil {
				log.Debug("config push failed", "client", s.t.client, "err", err)
			}
		}
		s.pushing.Store(false)
		// A kick between the last Swap and the Store left it to this
		// pusher; take it back unless a new pusher already did.
		if !s.dirty.Load() || !s.pushing.CompareAndSwap(false, true) {
			return
		}
	}
}

// configWriteTimeout bounds pushing one snapshot to a peer that stopped
// reading.
const configWriteTimeout = 30 * time.Second

// pushConfig opens one stream carrying a full snapshot (§4.3). It waits for
// stream credit until ctx or the session ends, so a push is never dropped;
// the kicks that arrive meanwhile are answered by the next snapshot.
func (s *serverSession) pushConfig(ctx context.Context, snap *wire.Snapshot) error {
	st, err := s.zc.OpenUniStreamSync(ctx)
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
