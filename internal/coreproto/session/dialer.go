package session

import (
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/lifecycle"
	"github.com/djylb/timeshim"
	"github.com/quic-go/quic-go"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/seal/proto"
	"github.com/zmuxio/zmux-go/zmuxws"
)

// boundLimit bounds an attempt in Bound before Sealed on a new carrier (§3.2).
const boundLimit = 5 * time.Second

// boundFailedTTL is how long a failed Bound is remembered (§3.2).
const boundFailedTTL = time.Hour

// maxConfigBody is the default bound of a CONFIG snapshot (§4.3).
const maxConfigBody = 16 << 20

// maxIncomingLinks is the credit of links the server may have open on one
// session; the old protocol had no limit, and it is the visitors that count.
const maxIncomingLinks = 1 << 30

// DialConfig is one server profile of npc.
type DialConfig struct {
	Candidates []connprofile.Candidate // from connprofile.Parse
	VKey       string
	Pin        []byte           // optional seal.StaticKeyID of the server
	Key        *ecdh.PrivateKey // nil: the key seal generates once per process
	Agent      string
	Label      string
	Timeout    time.Duration
	// Dial connects to the server, keeping -proxy and -local_ip; nil dials
	// directly.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// DialQUIC opens the QUIC connection of a quic carrier, keeping
	// -local_ip; nil dials directly.
	DialQUIC func(ctx context.Context, addr string, t *tls.Config, q *quic.Config) (*quic.Conn, error)
	// Scheduler names the sending scheduler (connprofile.Schedulers);
	// "" is affinity. A scheduler_up of CONFIG replaces it (§5).
	Scheduler string
	// Pipes is connprofile.PipesMulti for several pipes in a session; any
	// other value is one pipe, the candidates tried in order.
	Pipes string
	// MaxLanes is session_max_lanes: 0 is DefaultMaxLanes, 1 no lanes.
	MaxLanes int
	// Exit serves the links the server opens; nil refuses them.
	Exit coreapi.Exit
	// PeerExit makes the exit of a link this client provides under its
	// policy (§9.3); nil refuses LINK.
	PeerExit func(coreapi.LinkPolicy) (coreapi.Exit, error)
	// Puncher makes the direct paths of links; nil is relay only.
	Puncher coreapi.Puncher
	// CompressLimits are the receive limits of compressed streams (§7.4).
	CompressLimits CompressLimits
	// CanUpdate advertises the update capability; set it only when Update
	// can start an update, so that the server's interface offers it only then.
	CanUpdate bool
	// Update decides an update request of the server (§6) and starts the
	// update when it returns true; nil refuses them.
	Update func() bool
	// Health makes the runner of a session's checks (§5.3); nil runs none.
	Health func(coreapi.HealthSink) coreapi.HealthRunner
	// MaxConfig bounds a CONFIG snapshot; 0 is 16 MiB (§4.3).
	MaxConfig int
	// ResumeTimeout is how long a session that lost all its pipes waits for
	// one (§3.8); 0 is 30 s, negative is no resume. A resume_timeout_ms of
	// CONFIG replaces it from the next session on (§5).
	ResumeTimeout time.Duration
	// UsageInterval is how often a provider reports the direct traffic of
	// its links (§9.6); 0 is DefaultUsageInterval, negative is never.
	UsageInterval time.Duration
}

// Dialer establishes the sessions of a profile.
type Dialer struct {
	// tlsCache resumes the TLS of carriers (TLS 1.3 tickets): a redial or a
	// further pipe skips the full handshake; seal authenticates inside it
	// all the same.
	tlsCache    tls.ClientSessionCache
	clock       timeshim.Clock       // of the redial waits; tests replace it
	autoOK      map[string]bool      // type=auto entries that connected, per network
	autoDropped map[string]bool      // those whose session soon ended
	jitter      func() float64       // in [0, 1); tests replace it
	boundFailed map[string]time.Time // (candidate, network) where Bound failed and Sealed worked, when
	plans       *planSlot            // of this server and key
	now         func() time.Time     // tests
	recovery    *recoveryGate        // processRecovery; tests replace it
	h3Clients   map[string]*http.Client
	h3Known     map[string]bool                                                      // candidates on which h3 has worked (§2.4b)
	attempt     func(context.Context, connprofile.Candidate) (*clientSession, error) // tests replace it
	autoWinner  string                                                               // the entry of the session being kept, for pipes=multi
	last        string                                                               // candidate that connected last
	closers     []func()                                                             // of the HTTP clients the last session kept
	cfg         DialConfig
	kinds       zmux.CarrierCache // HTTP carrier kinds per candidate (§14)
	limits      CompressLimits
	record      uint64 // last CONFIG record
	resumeMs    uint64 // last CONFIG resume_timeout_ms, 0 for none
	mu          sync.Mutex
	// visitorOnly marks the sessions dialed from now on as pure visitors
	// (wire.Hello.VisitorOnly); OuterSessions sets it before each dial.
	visitorOnly atomic.Bool
}

// NewDialer returns a Dialer for cfg.
func NewDialer(cfg DialConfig) *Dialer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Dial == nil {
		cfg.Dial = (&net.Dialer{}).DialContext
	}
	if cfg.MaxConfig <= 0 {
		cfg.MaxConfig = maxConfigBody
	}
	d := &Dialer{cfg: cfg, autoOK: map[string]bool{}, autoDropped: map[string]bool{}, boundFailed: map[string]time.Time{}, h3Known: map[string]bool{}, h3Clients: map[string]*http.Client{}, now: time.Now, limits: cfg.CompressLimits.normalized(),
		tlsCache: tls.NewLRUClientSessionCache(0), recovery: processRecovery, clock: timeshim.Real, jitter: rand.Float64}
	d.attempt = d.dialCandidate
	d.plans = planSlotOf(d.Server() + "\x00" + cfg.VKey)
	return d
}

// Server implements coreapi.Dialer.
func (d *Dialer) Server() string {
	parts := make([]string, len(d.cfg.Candidates))
	for i, c := range d.cfg.Candidates {
		parts[i] = c.Raw
	}
	return strings.Join(parts, "|")
}

// Dial implements coreapi.Dialer: the candidates in order, the first that
// establishes.
func (d *Dialer) Dial(ctx context.Context) (coreapi.ClientSession, error) {
	d.mu.Lock()
	pool := d.cfg.Candidates
	if d.multi() {
		pool = connprofile.MultiMembers(pool)
	}
	cands := d.attempts(pool)
	d.mu.Unlock()
	if slices.ContainsFunc(cands, func(c connprofile.Candidate) bool { return c.Auto }) {
		s, err := d.dialRace(ctx, cands)
		if err != nil {
			d.closeHTTP()
			return nil, err
		}
		return s, nil
	}
	var errs []error
	for _, c := range cands {
		dctx, cancel := context.WithTimeout(ctx, d.attemptTimeout(c))
		s, err := d.dialCandidate(dctx, c)
		cancel()
		d.noteH3(c, err == nil, err)
		if err == nil {
			d.mu.Lock()
			d.last = c.Key()
			d.mu.Unlock()
			return s, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", c.Raw, err))
		if ctx.Err() != nil {
			break
		}
	}
	d.closeHTTP()
	if len(errs) == 0 {
		return nil, errors.New("no candidate can start a session")
	}
	return nil, errors.Join(errs...)
}

// carrier is a dialed carrier and what the mode choice needs of it.
type carrier struct {
	pipe    zmux.PipeIO
	secure  bool   // end-to-end TLS of ours: Bound is possible
	network string // the local network the carrier left by (localNetwork)
}

// plan is the record mode of an attempt.
type plan struct {
	mode     zmux.RecordMode
	fallback bool // a failing Bound goes to Sealed on a new carrier
}

// choose applies §3.2: the delivered record, or the "on" rules without one.
func choose(record uint64, secure, boundFailed bool) plan {
	switch record {
	case wire.RecordTrusted:
		return plan{mode: zmux.RecordTrusted}
	case wire.RecordSealed:
		return plan{mode: zmux.RecordSealed}
	case wire.RecordBound:
		if secure {
			return plan{mode: zmux.RecordBound}
		}
		return plan{mode: zmux.RecordSealed}
	}
	if secure && !boundFailed {
		return plan{mode: zmux.RecordBound, fallback: true}
	}
	return plan{mode: zmux.RecordSealed}
}

// dialCandidate dials c and establishes; a ws or wss URL candidate that names
// no subprotocol and gets no reply to Establish may have reached the old
// protocol of the shared bridge path, which the error says.
func (d *Dialer) dialCandidate(ctx context.Context, c connprofile.Candidate) (*clientSession, error) {
	s, err := d.dialCandidateOnce(ctx, c)
	if err != nil && errors.Is(err, seal.ErrNoReply) && ctx.Err() == nil &&
		(c.Carrier == "ws" || c.Carrier == "wss") && c.Subprotocol == "" && strings.Contains(c.Raw, "://") {
		err = fmt.Errorf("%w (the shared bridge path needs subprotocol=binary)", err)
	}
	if err != nil && errors.Is(err, seal.ErrNoReply) && c.Carrier == "tcp" {
		err = fmt.Errorf("%w: %w", coreapi.ErrPlainRefused, err)
	}
	return s, err
}

func (d *Dialer) dialCandidateOnce(ctx context.Context, c connprofile.Candidate) (*clientSession, error) {
	ca, err := d.dialCarrier(ctx, c)
	if err != nil {
		return nil, err
	}
	key := c.Key() + "|" + ca.network
	d.mu.Lock()
	p := choose(d.record, ca.secure, d.boundFailedLocked(key))
	d.mu.Unlock()
	if p.mode != zmux.RecordBound {
		return d.establish(ctx, ca, p.mode, c)
	}
	bctx, cancel := context.WithTimeout(ctx, boundLimit)
	s, err := d.establish(bctx, ca, zmux.RecordBound, c)
	cancel()
	if err == nil || !p.fallback || ctx.Err() != nil {
		return s, err
	}
	log.Debug("bound failed, trying sealed", "carrier", c.Carrier, "err", err)
	if ca, err = d.dialCarrier(ctx, c); err != nil {
		return nil, err
	}
	if s, err = d.establish(ctx, ca, zmux.RecordSealed, c); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.noteBoundFailedLocked(key)
	d.mu.Unlock()
	return s, nil
}

// boundFailedLocked reports whether Bound failed and Sealed worked for key
// within boundFailedTTL; an older entry is dropped.
func (d *Dialer) boundFailedLocked(key string) bool {
	at, ok := d.boundFailed[key]
	if ok && d.now().Sub(at) >= boundFailedTTL {
		delete(d.boundFailed, key)
		return false
	}
	return ok
}

// noteBoundFailedLocked records that Bound failed and Sealed worked for key,
// and drops the entries that expired.
func (d *Dialer) noteBoundFailedLocked(key string) {
	now := d.now()
	for k, at := range d.boundFailed {
		if now.Sub(at) >= boundFailedTTL {
			delete(d.boundFailed, k)
		}
	}
	d.boundFailed[key] = now
}

func (d *Dialer) dialCarrier(ctx context.Context, c connprofile.Candidate) (carrier, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: c.ALPN, ServerName: serverName(c), ClientSessionCache: d.tlsCache}
	switch c.Carrier {
	case "tcp", "tls":
		nc, err := d.cfg.Dial(ctx, "tcp", c.Addr)
		if err != nil {
			return carrier{}, err
		}
		ca := carrier{network: localNetwork(nc)}
		if c.Carrier == "tcp" {
			ca.pipe = zmux.NetPipe(nc)
			return ca, nil
		}
		//noinspection GoResourceLeak
		tc := tls.Client(nc, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return carrier{}, err
		}
		ca.pipe, ca.secure = zmux.SecureTLS(tc), true
		return ca, nil
	case "quic":
		return d.dialQUIC(ctx, c, tlsCfg)
	case "http", "https":
		return d.dialHTTP(ctx, c)
	case "ws", "wss":
		scheme := c.Carrier
		// The old way of writing a candidate sends the subprotocol (§12);
		// a URL candidate sends the one it names, else none.
		protocols := []string{zmuxws.NoSubprotocol}
		switch {
		case c.Subprotocol != "":
			protocols = []string{c.Subprotocol}
		case !strings.Contains(c.Raw, "://"):
			protocols = []string{legacySubprotocol}
		}
		cd := &captureDialer{dial: d.cfg.Dial}
		o := &zmuxws.DialOptions{Protocols: protocols, TLSConfig: tlsCfg, MaxRedirects: -1, Dialer: cd}
		if c.Host != "" {
			o.Header = http.Header{"Host": {c.Host}}
		}
		p, err := zmuxws.DialPipe(ctx, scheme+"://"+c.Addr+c.Path, o)
		if err != nil {
			return carrier{}, err
		}
		// HTTP/1.1 on a TLS connection of its own: zmuxws declares it.
		return carrier{pipe: p, secure: scheme == "wss" && zmux.IsCarrierSecure(p), network: cd.network()}, nil
	}
	return carrier{}, fmt.Errorf("carrier %q is not available on the new protocol yet", c.Carrier)
}

// captureDialer dials TCP and remembers the local network of the last
// connection, which zmuxws does not show.
type captureDialer struct {
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	mu   sync.Mutex
	net  string
}

func (d *captureDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	nc, err := d.dial(ctx, network, addr)
	if err == nil {
		d.mu.Lock()
		d.net = localNetwork(nc)
		d.mu.Unlock()
	}
	return nc, err
}

func (d *captureDialer) network() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.net
}

// localNetwork names the local network a connection left by: its local
// address and the interface that holds it (name, index, hardware address).
// A cache keyed by it ends with a move to another network, as far as that
// changes the address or the interface; two networks that hand out the same
// address on the same interface look alike (the default route, which would
// tell them apart, has no portable cheap source, so the one-hour limit
// bounds that case).
func localNetwork(c net.Conn) string {
	return networkOf(c.LocalAddr())
}

// networkOf is localNetwork of a local address, TCP or UDP.
func networkOf(addr net.Addr) string {
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	case *net.UDPAddr:
		ip = a.IP
	default:
		return ""
	}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, ia := range addrs {
			if ipn, ok := ia.(*net.IPNet); ok && ipn.IP.Equal(ip) {
				return fmt.Sprintf("%s@%s#%d/%s", ip, ifc.Name, ifc.Index, ifc.HardwareAddr)
			}
		}
	}
	return ip.String()
}

func serverName(c connprofile.Candidate) string {
	if c.SNI != "" {
		return c.SNI
	}
	h, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		h = c.Addr
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return ""
	}
	return h
}

// localIP is the IP address a carrier left from, "" when it has none to tell.
func localIP(p zmux.PipeIO) string {
	// the pipe's own connection, only looked at: the pipe closes it
	//noinspection GoResourceLeak
	a := zmux.PipeConn(p).LocalAddr()
	if a == nil {
		return ""
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil || ap.Addr().IsUnspecified() {
		return ""
	}
	return ap.Addr().WithZone("").Unmap().String()
}

// establish runs Establish and starts the session on ca. On a failure ca is
// closed.
func (d *Dialer) establish(ctx context.Context, ca carrier, mode zmux.RecordMode, c connprofile.Candidate) (*clientSession, error) {
	handle, psk, err := wire.Derive(d.cfg.VKey)
	if err != nil {
		return nil, err
	}
	var pin *proto.KeyID
	if len(d.cfg.Pin) > 0 {
		if len(d.cfg.Pin) != len(proto.KeyID{}) {
			return nil, errors.New("pin: wrong length")
		}
		id := proto.KeyID(d.cfg.Pin)
		pin = &id
	}
	visitorOnly := d.visitorOnly.Load()
	hello := clientHello(d.cfg.Agent, d.cfg.Label, localIP(ca.pipe), d.cfg.CanUpdate, visitorOnly)
	shape := d.plan()
	gate := raceGateOf(ctx)
	var held atomic.Bool
	release := func() {
		if held.CompareAndSwap(true, false) {
			gate.release()
		}
	}
	q, err := seal.Establish(ctx, ca.pipe, &seal.ClientConfig{
		Shaper:     establishShape(shape),
		Anchor:     &anchor{pin: pin},
		RecordMode: mode,
		Handle:     handle,
		PSK:        psk,
		StaticKey:  d.cfg.Key,
		MaxAppData: wire.MaxHelloRecv,
		AppData: func(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
			if r.Message == 3 {
				if gate != nil {
					// The Hello commits the server to this session: only
					// one of a race may send it.
					if err := gate.acquire(ctx); err != nil {
						return nil, err
					}
					held.Store(true)
				}
				return hello, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		release()
		// A carrier of several requests has no connection to end with it.
		if a, ok := ca.pipe.W.(interface{ Abort(error) }); ok {
			a.Abort(err)
		}
		_ = zmux.PipeConn(ca.pipe).Close()
		return nil, err
	}
	s := &clientSession{d: d, mode: mode, visitorOnly: visitorOnly, carrier: c.Carrier, plan: shape, boot: ca.pipe.Carrier, done: make(chan struct{}), configured: make(chan struct{}), wg: &sync.WaitGroup{}}
	if s.zc, err = zmux.Start(ctx, q, zmux.RoleClient, d.sessionConfig(s)); err != nil {
		release()
		return nil, closeCause(err)
	}
	s.start(c)
	return s, nil
}

// anchor is the client's Anchor (§3.3): any server that passed psk0, or
// exactly the pinned key.
type anchor struct{ pin *proto.KeyID }

func (a *anchor) Verify(_ context.Context, in seal.VerifyInput) (seal.VerifyVerdict, error) {
	id := seal.StaticKeyID(in.PeerStatic)
	if a.pin != nil && id != *a.pin {
		return seal.VerifyVerdict{}, seal.ErrVerifyRejected
	}
	return seal.VerifyVerdict{Principal: seal.Principal{Type: seal.PrincipalNodeKey, Bytes: id[:]}}, nil
}

// clientSession implements coreapi.ClientSession.
type clientSession struct {
	autoStart    time.Time // when it connected
	suspendSince time.Time // of the last EventSessionSuspended; only event uses it
	err          error
	ctx          context.Context             // of the callbacks, ended with the session
	healthRunner coreapi.HealthRunner        // made by the first snapshot with checks; under mu
	configured   chan struct{}               // closed by the first snapshot
	onApply      func(uint64, wire.Snapshot) // tests; under mu
	d            *Dialer
	provided     map[uint64]*providerLink // links this client provides, by id; under mu
	zc           *zmux.Conn
	plan         *shapingPlan // the bundle in use; under mu
	done         chan struct{}
	wg           *sync.WaitGroup                 // the pipe pools of a multi-pipe session
	scope        *lifecycle.Scope                // the zmux callbacks (start)
	services     map[string]coreapi.LocalService // what this session offers, by id; under mu
	redialBound  map[string]bool                 // candidate|network of Bound tries of unknown result; under redialMu
	carrier      string
	redialKey    string // candidate of the carrier given to zmux last; under redialMu
	autoEntry    string // the race entry of the session, "" for none
	serviceSeq   uint64 // seq of the last SERVICE snapshot; under mu
	redialTries  int    // redials of this suspension (redialWait); under redialMu
	applied      uint64 // seq of the applied snapshot; under mu

	configuredOnce sync.Once

	// The redial of the session's only pipe. redialMu is held alone, and
	// guards redialKey, redialMode, redialBound and redialTries.
	redialMu   sync.Mutex
	mu         sync.Mutex      // guards provided, applied, services, serviceSeq and onApply
	redialMode zmux.RecordMode // its mode (Config.RecordPolicy); under redialMu

	visitorOnly bool // dialed as a pure visitor
	mode        zmux.RecordMode
	boot        zmux.CarrierKind // of the first pipe
}

func (s *clientSession) start(boot connprofile.Candidate) {
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	// The session runs on zmux callbacks, each a task of s.scope; only the
	// pipe pools of a multi-pipe session keep goroutines (s.wg).
	s.scope = newSessionScope()
	if err := handleStreams(s.zc, s.scope, func(st *zmux.Stream) { s.serveStream(ctx, st) }); err != nil {
		log.Debug("session streams not served", "err", err)
	}
	if err := s.zc.HandleUniStreams(s.receiveTask, unboundedHandlers); err != nil {
		log.Debug("session uni streams not served", "err", err)
	}
	if err := s.zc.HandleEvents(s.event); err != nil {
		log.Debug("session events not watched", "err", err)
	}
	if s.d.multi() {
		s.wg.Go(func() { s.maintain(ctx, boot) })
	}
	go func() {
		s.err = closeCause(s.zc.Wait())
		cancel()
		// No callback starts past Drain, and the running ones end before
		// the cleanup below: zmux's Wait waits for none of them. They add
		// nothing to s.wg, which is waited for after them.
		s.scope.Drain()
		_ = s.scope.Wait(context.Background())
		s.wg.Wait()
		// nothing applies snapshots any more
		if s.healthRunner != nil {
			s.healthRunner.Stop()
		}
		s.ended()
		s.d.autoEnded(s)
		s.d.closeHTTP()
		close(s.done)
	}()
}

// receiveTask is receive as a task of the session's scope; a stream that
// comes once the scope drains is refused like one of an unknown op.
func (s *clientSession) receiveTask(st *zmux.ReceiveStream) {
	task, err := s.scope.Enter()
	if err != nil {
		st.CancelRead(zmux.StreamErrorCode(wire.CodeDrain))
		return
	}
	defer task.End()
	s.receive(st)
}

// ended applies §3.2: Trusted lost before any CONFIG forgets the delivered
// record, so that a switch turned on while offline can take effect.
func (s *clientSession) ended() {
	s.mu.Lock()
	applied := s.applied
	s.mu.Unlock()
	if s.mode == zmux.RecordTrusted && applied == 0 {
		s.d.setRecord(wire.RecordDefault)
	}
}

// resumeTimeout is the grace of the next session.
func (d *Dialer) resumeTimeout() time.Duration {
	d.mu.Lock()
	ms := d.resumeMs
	d.mu.Unlock()
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return resumeTimeoutOf(d.cfg.ResumeTimeout)
}

func (d *Dialer) setRecord(r uint64) {
	d.mu.Lock()
	d.record = r
	d.mu.Unlock()
}

func (d *Dialer) setResumeMs(ms uint64) {
	d.mu.Lock()
	d.resumeMs = ms
	d.mu.Unlock()
}

// receive handles a unidirectional stream: CONFIG, or a refusal (§4.3, §11).
func (s *clientSession) receive(st *zmux.ReceiveStream) {
	op, fields, err := wire.ParseOpenOp(st.Preamble())
	if err != nil || op != wire.OpConfig {
		st.CancelRead(zmux.StreamErrorCode(rejectCode(st.Preamble(), wire.Op.FromServer)))
		return
	}
	seq, err := wire.ParseConfigOpen(fields)
	if err != nil {
		st.CancelRead(zmux.StreamErrorCode(wire.ErrorCode(err)))
		return
	}
	limit := s.d.cfg.MaxConfig
	body, err := io.ReadAll(io.LimitReader(st, int64(limit)+1))
	if err != nil {
		return
	}
	if len(body) > limit {
		st.CancelRead(zmux.StreamErrorCode(wire.CodeMalformed))
		return
	}
	snap, err := wire.ParseSnapshot(body)
	if err != nil {
		st.CancelRead(zmux.StreamErrorCode(wire.ErrorCode(err)))
		return
	}
	s.apply(seq, snap)
}

// apply takes a snapshot unless a newer one was applied (§4.3).
func (s *clientSession) apply(seq uint64, snap wire.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq <= s.applied {
		return
	}
	s.applied = seq
	s.d.setRecord(snap.Record)
	s.d.setResumeMs(snap.Session.ResumeMs)
	s.applySessionOptions(snap.Session)
	s.applyHealth(snap.Health)
	s.applyShaping(snap.Shaping)
	if snap.HasSchedulerUp {
		s.zc.SetScheduler(schedulerNamed(connprofile.Schedulers[snap.SchedulerUp]))
	}
	if s.configured != nil {
		s.configuredOnce.Do(func() { close(s.configured) })
	}
	if s.onApply != nil {
		s.onApply(seq, snap)
	}
}

// livenessOf is §5: idle_s is how often an idle pipe is checked, dead_s when
// a suspect one is given up. Neither has a default of its own: without them
// zmux uses its defaults.
func livenessOf(o wire.SessionOptions) zmux.Liveness {
	idle := time.Duration(o.IdleS) * time.Second
	return zmux.Liveness{KeepAlivePeriod: idle, ProbeInterval: idle, DeadTimeout: time.Duration(o.DeadS) * time.Second}
}

// applySessionOptions follows the server's timers (§5).
func (s *clientSession) applySessionOptions(o wire.SessionOptions) {
	if o.IdleS == 0 && o.DeadS == 0 {
		return
	}
	if err := s.zc.SetLiveness(livenessOf(o)); err != nil {
		log.Debug("session options not applied", "err", err)
	}
}

func (s *clientSession) Carrier() string { return s.carrier }

// RecordMode names the record mode of the session's first pipe.
func (s *clientSession) RecordMode() string    { return s.mode.String() }
func (s *clientSession) Done() <-chan struct{} { return s.done }
func (s *clientSession) Err() error            { return s.err }
func (s *clientSession) Close() error          { return s.zc.Close() }

// closeCause marks the server's close codes that the client acts on with the
// coreapi sentinels, so that the client needs no zmux or wire type.
func closeCause(err error) error {
	ae, ok := errors.AsType[*zmux.ApplicationError](err)
	if !ok || !ae.Remote {
		return err
	}
	switch uint64(ae.ErrorCode) {
	case wire.CodeDrain:
		return fmt.Errorf("%w: %w", coreapi.ErrDrained, err)
	case wire.CodeAuth, wire.CodeDisabled:
		return fmt.Errorf("%w: %w", coreapi.ErrAuthClosed, err)
	}
	return err
}
