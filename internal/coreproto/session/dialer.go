package session

import (
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

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
	Pin        []byte           // optional seal.NodeKeyID of the server
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
}

// Dialer establishes the sessions of a profile.
type Dialer struct {
	cfg DialConfig

	mu          sync.Mutex
	record      uint64               // last CONFIG record
	resumeMs    uint64               // last CONFIG resume_timeout_ms, 0 for none
	boundFailed map[string]time.Time // (candidate, network) where Bound failed and Sealed worked, when
	last        string               // candidate that connected last
	now         func() time.Time     // tests
	limits      CompressLimits
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
	return &Dialer{cfg: cfg, boundFailed: map[string]time.Time{}, now: time.Now, limits: cfg.CompressLimits.normalized()}
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
	cands := connprofile.Order(pool, d.last)
	d.mu.Unlock()
	var errs []error
	for _, c := range cands {
		if !c.Starts() {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
		s, err := d.dialCandidate(dctx, c)
		cancel()
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
		return plan{mode: zmux.Trusted}
	case wire.RecordSealed:
		return plan{mode: zmux.Sealed}
	case wire.RecordBound:
		if secure {
			return plan{mode: zmux.Bound}
		}
		return plan{mode: zmux.Sealed}
	}
	if secure && !boundFailed {
		return plan{mode: zmux.Bound, fallback: true}
	}
	return plan{mode: zmux.Sealed}
}

func (d *Dialer) dialCandidate(ctx context.Context, c connprofile.Candidate) (*clientSession, error) {
	ca, err := d.dialCarrier(ctx, c)
	if err != nil {
		return nil, err
	}
	key := c.Key() + "|" + ca.network
	d.mu.Lock()
	p := choose(d.record, ca.secure, d.boundFailedLocked(key))
	d.mu.Unlock()
	if p.mode != zmux.Bound {
		return d.establish(ctx, ca, p.mode, c)
	}
	bctx, cancel := context.WithTimeout(ctx, boundLimit)
	s, err := d.establish(bctx, ca, zmux.Bound, c)
	cancel()
	if err == nil || !p.fallback || ctx.Err() != nil {
		return s, err
	}
	log.Debug("bound failed, trying sealed", "carrier", c.Carrier, "err", err)
	if ca, err = d.dialCarrier(ctx, c); err != nil {
		return nil, err
	}
	if s, err = d.establish(ctx, ca, zmux.Sealed, c); err != nil {
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
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: c.ALPN, ServerName: serverName(c)}
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
		tc := tls.Client(nc, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return carrier{}, err
		}
		ca.pipe, ca.secure = zmux.SecureTLS(tc), true
		return ca, nil
	case "quic":
		return d.dialQUIC(ctx, c, tlsCfg)
	case "ws", "wss":
		scheme := c.Carrier
		// Only the old way of writing a candidate sends the subprotocol
		// (§12); URL candidates send none.
		protocols := []string{zmuxws.NoSubprotocol}
		if !strings.Contains(c.Raw, "://") {
			protocols = []string{legacySubprotocol}
		}
		cd := &captureDialer{dial: d.cfg.Dial}
		o := &zmuxws.DialOptions{Protocols: protocols, TLSConfig: tlsCfg, NoRedirects: true, Dialer: cd}
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
	hello := clientHello(d.cfg.Agent, d.cfg.Label)
	q, err := seal.Establish(ctx, ca.pipe, &seal.ClientConfig{
		Anchor:     &anchor{pin: pin},
		Mode:       mode,
		Handle:     handle,
		PSK:        psk,
		Key:        d.cfg.Key,
		MaxAppData: wire.MaxHelloRecv,
		AppData: func(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
			if r.Message == 3 {
				return hello, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		return nil, err
	}
	s := &clientSession{d: d, mode: mode, carrier: c.Carrier, done: make(chan struct{}), configured: make(chan struct{}), wg: &sync.WaitGroup{}}
	if s.zc, err = zmux.Start(ctx, q, zmux.RoleClient, d.sessionConfig(s)); err != nil {
		return nil, err
	}
	s.start(c)
	return s, nil
}

// anchor is the client's Anchor (§3.3): any server that passed psk0, or
// exactly the pinned key.
type anchor struct{ pin *proto.KeyID }

func (a *anchor) Verify(_ context.Context, in seal.VerifyInput) (seal.Verdict, error) {
	id := seal.NodeKeyID(in.PeerStatic)
	if a.pin != nil && id != *a.pin {
		return seal.Verdict{}, seal.ErrVerifyRejected
	}
	return seal.Verdict{Principal: seal.Principal{Type: seal.PrincipalNodeKey, Bytes: id[:]}}, nil
}

// clientSession implements coreapi.ClientSession.
type clientSession struct {
	d       *Dialer
	zc      *zmux.Conn
	mode    zmux.RecordMode
	carrier string
	done    chan struct{}
	err     error

	configured     chan struct{} // closed by the first snapshot
	configuredOnce sync.Once

	healthRunner coreapi.HealthRunner // made by the first snapshot with checks; under mu

	wg *sync.WaitGroup

	// The redial of the session's only pipe. redialMu is held alone.
	redialMu    sync.Mutex
	redialKey   string          // candidate of the carrier given to zmux last
	redialMode  zmux.RecordMode // its mode (Config.RecordPolicy)
	redialBound map[string]bool // candidate|network of Bound tries of unknown result

	mu         sync.Mutex
	provided   map[uint64]*providerLink        // links this client provides, by id
	applied    uint64                          // seq of the applied snapshot
	services   map[string]coreapi.LocalService // what this session offers, by id
	serviceSeq uint64                          // seq of the last SERVICE snapshot
	onApply    func(uint64, wire.Snapshot)     // tests
}

func (s *clientSession) start(boot connprofile.Candidate) {
	ctx, cancel := context.WithCancel(context.Background())
	s.wg.Go(func() { s.serveStreams(ctx) })
	s.wg.Go(func() { s.watch(ctx) })
	if s.d.multi() {
		s.wg.Go(func() { s.maintain(ctx, boot) })
	}
	s.wg.Go(func() {
		for {
			st, err := s.zc.AcceptUniStream(ctx)
			if err != nil {
				return
			}
			s.wg.Go(func() { s.receive(st) })
		}
	})
	go func() {
		s.err = s.zc.Wait()
		cancel()
		s.wg.Wait()
		// nothing applies snapshots any more
		if s.healthRunner != nil {
			s.healthRunner.Stop()
		}
		s.ended()
		close(s.done)
	}()
}

// ended applies §3.2: Trusted lost before any CONFIG forgets the delivered
// record, so that a switch turned on while offline can take effect.
func (s *clientSession) ended() {
	s.mu.Lock()
	applied := s.applied
	s.mu.Unlock()
	if s.mode == zmux.Trusted && applied == 0 {
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

func (s *clientSession) Carrier() string       { return s.carrier }
func (s *clientSession) Done() <-chan struct{} { return s.done }
func (s *clientSession) Err() error            { return s.err }
func (s *clientSession) Close() error          { return s.zc.Close() }
