package session

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxquic"
)

const (
	// DefaultRelayIdle is how long a relay stands by after the direct path
	// came up (§9.4a).
	DefaultRelayIdle = time.Minute
	visitorTick      = time.Second
	punchMaxBackoff  = time.Minute
)

// OuterSessions keeps the one session of a pure visitor to the server: dialed
// on demand and closed when no visitor needs it (§9.4a).
type OuterSessions struct {
	d *Dialer

	dial sync.Mutex // serializes dialing
	mu   sync.Mutex
	cur  *clientSession
	need map[*Visitor]bool
	// Hosting reports tunnels or virtual services this npc serves through
	// the session; nil is none.
	Hosting func() bool
	// held is set by Hold: the session's role then follows Hosting, and a
	// session dialed while nothing hosts is a pure visitor. Without Hold
	// (one-off commands) the session is an ordinary one.
	held bool
	// Keep holds the session open whatever the visitors need
	// (p2p_visitor_keep_server).
	Keep bool
}

// visitorOnly reports, with o.mu held, whether a session dialed now only
// visits.
func (o *OuterSessions) visitorOnly() bool {
	return o.held && (o.Hosting == nil || !o.Hosting())
}

// mayDisconnect is the one decision whether the session may close when idle
// (§9.4a), with o.mu held: nothing needs it now and nothing the server or a
// peer may ask of this client depends on it.
func (o *OuterSessions) mayDisconnect() bool {
	if o.Keep || len(o.need) > 0 || o.Hosting != nil && o.Hosting() {
		return false
	}
	return o.cur == nil || !o.cur.providing()
}

// NewOuterSessions returns the holder of d's session.
func NewOuterSessions(d *Dialer) *OuterSessions {
	return &OuterSessions{d: d, need: map[*Visitor]bool{}}
}

// live returns the session if it is up and of the role needed now. A pure
// visitor session that a host has come to use is closed, so that the host
// gets a session of its own role; the visitors on it make their links again.
func (o *OuterSessions) live() *clientSession {
	o.mu.Lock()
	cur := o.cur
	if cur != nil && cur.zc.Context().Err() == nil {
		if !cur.visitorOnly || o.visitorOnly() {
			o.mu.Unlock()
			return cur
		}
	} else {
		cur = nil
	}
	o.cur = nil
	o.mu.Unlock()
	if cur != nil {
		_ = cur.Close()
	}
	return nil
}

// get returns the session, dialing it if there is none.
func (o *OuterSessions) get(ctx context.Context) (*clientSession, error) {
	if s := o.live(); s != nil {
		return s, nil
	}
	o.dial.Lock()
	defer o.dial.Unlock()
	if s := o.live(); s != nil {
		return s, nil
	}
	o.mu.Lock()
	visitorOnly := o.visitorOnly()
	o.mu.Unlock()
	o.d.visitorOnly.Store(visitorOnly)
	cs, err := o.d.Dial(ctx)
	if err != nil {
		if visitorOnly && ctx.Err() == nil {
			log.Warn("visitor session to server failed", "server", o.d.Server(), "err", err)
		}
		return nil, err
	}
	s := cs.(*clientSession)
	if visitorOnly {
		log.Info("visitor session to server connected", "server", o.d.Server(), "carrier", s.carrier)
	}
	o.mu.Lock()
	o.cur = s
	o.mu.Unlock()
	return s, nil
}

// Dial implements coreapi.Dialer with the session itself: a host keeps the
// session through it, and the visitors that share it keep theirs.
func (o *OuterSessions) Dial(ctx context.Context) (coreapi.ClientSession, error) {
	s, err := o.get(ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Server implements coreapi.Dialer.
func (o *OuterSessions) Server() string { return o.d.Server() }

// Visitor implements coreapi.Outer.
func (o *OuterSessions) Visitor(access []byte, mode string, relayIdle time.Duration, serverExit bool) coreapi.Visitor {
	return NewVisitor(VisitorConfig{Outer: o, Access: access, Mode: ConnModeOf(mode), RelayIdle: relayIdle, ServerExit: serverExit})
}

// Hold implements coreapi.Outer. It is called before the session is used.
func (o *OuterSessions) Hold(keep bool, hosting func() bool) {
	o.mu.Lock()
	o.Keep, o.Hosting, o.held = keep, hosting, true
	o.mu.Unlock()
}

// setNeed records whether v needs the session, closing it when nobody does.
func (o *OuterSessions) setNeed(v *Visitor, need bool) {
	o.mu.Lock()
	if need {
		o.need[v] = true
	} else {
		delete(o.need, v)
	}
	var idle *clientSession
	if o.mayDisconnect() {
		idle, o.cur = o.cur, nil
	}
	o.mu.Unlock()
	if idle != nil {
		_ = idle.Close()
	}
}

// Close ends the session.
func (o *OuterSessions) Close() {
	o.mu.Lock()
	s := o.cur
	o.cur = nil
	o.mu.Unlock()
	if s != nil {
		_ = s.Close()
		<-s.Done()
	}
}

// VisitorConfig is one link a visitor reaches.
type VisitorConfig struct {
	Outer      *OuterSessions
	Access     []byte
	Mode       uint64        // wire.ConnRelay, ConnAuto or ConnDirect
	ServerExit bool          // ask for the server as the exit of the link
	RelayIdle  time.Duration // 0 is DefaultRelayIdle
	Tick       time.Duration // 0 is one second; tests
	Timeout    time.Duration // 0 is 15 s: making the link
}

// Visitor implements coreapi.Visitor: the link, made on demand and made again
// when it ended.
type Visitor struct {
	cfg    VisitorConfig
	ctx    context.Context
	cancel context.CancelFunc

	make sync.Mutex // serializes making the link
	mu   sync.Mutex
	cur  *visitorLink
}

// NewVisitor returns the access cfg describes.
func NewVisitor(cfg VisitorConfig) *Visitor {
	if cfg.RelayIdle <= 0 {
		cfg.RelayIdle = DefaultRelayIdle
	}
	if cfg.Tick <= 0 {
		cfg.Tick = visitorTick
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Mode == 0 {
		cfg.Mode = wire.ConnAuto
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Visitor{cfg: cfg, ctx: ctx, cancel: cancel}
}

// Close implements coreapi.Visitor.
func (v *Visitor) Close() error {
	v.cancel()
	v.mu.Lock()
	l := v.cur
	v.cur = nil
	v.mu.Unlock()
	if l != nil {
		l.close()
	}
	v.cfg.Outer.setNeed(v, false)
	return nil
}

func (v *Visitor) live() *visitorLink {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cur != nil && !v.cur.ended() {
		return v.cur
	}
	return nil
}

func (v *Visitor) link(ctx context.Context) (*visitorLink, error) {
	if l := v.live(); l != nil {
		return l, nil
	}
	v.make.Lock()
	defer v.make.Unlock()
	if l := v.live(); l != nil {
		return l, nil
	}
	if err := v.ctx.Err(); err != nil {
		return nil, err
	}
	mctx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()
	l, err := v.makeLink(mctx)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.cur = l
	v.mu.Unlock()
	return l, nil
}

// OpenLink implements coreapi.Visitor.
func (v *Visitor) OpenLink(ctx context.Context, r coreapi.LinkRequest) (net.Conn, error) {
	l, err := v.link(ctx)
	if err != nil {
		return nil, err
	}
	if l.exit != nil {
		r.LinkID = l.id
		return openLink(ctx, l.exitSession.zc, r, nil, l.exitSession.d.limits)
	}
	return openLink(ctx, l.zc, r, l.peerAlgs, v.cfg.Outer.d.limits)
}

// peerCompressAlgs reads compress_alg of the provider's Hello (§9.3).
func peerCompressAlgs(q zmux.PipeIO) []uint64 {
	peer, ok := seal.PeerOf(q)
	if !ok {
		return nil
	}
	for _, d := range peer.AppData {
		if h, err := wire.ParseHello(d.Data); err == nil {
			return h.CompressAlgs
		}
	}
	return nil
}

// visitorLink is a live link: the nested session and its paths.
type visitorLink struct {
	v    *Visitor
	id   uint64
	mode uint64
	key  []byte

	// server_exit only: the LINK stream that keeps the link and its session.
	exit        *linkConn
	exitSession *clientSession
	exitDone    chan struct{} // closed when the LINK stream ended

	zc     *zmux.Conn
	shape  *nestedShaping
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	peerAlgs []uint64 // what the provider decompresses (§7.2), set once

	mu       sync.Mutex
	punching bool
	// refused: the server said the link is gone (refusalReader).
	refused atomic.Bool
	direct  *coreapi.DirectPath
	upSince time.Time           // the direct path up, continuously
	upPath  *coreapi.DirectPath // the path that was up
	backoff time.Duration
	nextTry time.Time
}

func (l *visitorLink) ended() bool {
	if l.exit != nil {
		select {
		case <-l.exitDone:
			return true
		default:
			return false
		}
	}
	return l.zc.Context().Err() != nil
}

func (l *visitorLink) close() {
	if l.exit != nil {
		_ = l.exit.Close()
		return
	}
	l.cancel()
	_ = l.zc.Close()
	l.wg.Wait()
}

// makeLink runs LINK and opens the first path.
func (v *Visitor) makeLink(ctx context.Context) (_ *visitorLink, err error) {
	v.cfg.Outer.setNeed(v, true)
	defer func() {
		if err != nil {
			v.cfg.Outer.setNeed(v, false)
		}
	}()
	s, err := v.cfg.Outer.get(ctx)
	if err != nil {
		return nil, err
	}
	// The key never goes on the wire: its proof is bound to this session (§9.1).
	ekm, err := s.zc.ExportKeyingMaterial(wire.AccessExportLabel, nil, wire.AccessExportLen)
	if err != nil {
		return nil, err
	}
	info, err := wire.AppendLinkOpen(&wire.LinkOpen{Access: wire.AppendAccess(v.cfg.Access, ekm), ConnMode: v.cfg.Mode, ServerExit: v.cfg.ServerExit})
	if err != nil {
		return nil, err
	}
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	lc := &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
	if err := st.SetPreamble(info); err != nil || st.Flush() != nil {
		lc.abort(wire.CodeInternal)
		return nil, errors.New("link not opened")
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = lc.SetReadDeadline(dl)
	}
	rep, err := wire.ReadReply(lc, replyLimit)
	if err != nil {
		lc.abort(wire.CodeInternal)
		return nil, replyReadError(err)
	}
	if rep.Code != wire.CodeOK {
		lc.abort(wire.CodeInternal)
		return nil, linkError(rep.Code)
	}
	r, err := wire.ParseLinkReply(rep.Fields)
	if err != nil {
		lc.abort(wire.CodeMalformed)
		return nil, err
	}
	_ = lc.SetReadDeadline(time.Time{})
	l := &visitorLink{v: v, id: r.LinkID, mode: r.ConnMode, key: r.LinkKey}
	if r.ServerExit {
		l.exit, l.exitSession = lc, s
		l.exitDone = make(chan struct{})
		go func() {
			// The stream's end is the revocation; its Context is the session's.
			_, _ = io.Copy(io.Discard, lc)
			close(l.exitDone)
			v.linkOver(l)
		}()
		return l, nil
	}
	_ = lc.Close()
	if l.mode == 0 {
		l.mode = v.cfg.Mode
	}
	if err := l.start(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// start establishes the nested session on the first path (§9.3).
func (l *visitorLink) start(ctx context.Context) error {
	var pipe zmux.PipeIO
	var err error
	mode := zmux.Sealed
	if l.mode == wire.ConnDirect {
		mode = zmux.Bound
		pipe, err = l.punch(ctx, false)
	} else {
		pipe, err = l.openRelay(ctx)
	}
	if err != nil {
		return err
	}
	handle, psk, err := seal.DeriveHandlePSK(l.key, []byte(linkDomain), nil)
	if err != nil {
		return err
	}
	shape := l.v.cfg.Outer.d.nestedShaping()
	q, err := seal.Establish(ctx, pipe, &seal.ClientConfig{Shaper: shape.slot.handshake, Anchor: &anchor{}, Mode: mode, Handle: handle, PSK: psk, MaxAppData: wire.MaxHelloRecv,
		AppData: func(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
			if r.Message == 3 {
				return wire.AppendHello(nil, &wire.Hello{CompressAlgs: compressAlgs}), nil
			}
			return nil, nil
		}})
	if err != nil {
		l.closeDirect()
		return err
	}
	l.peerAlgs = peerCompressAlgs(q)
	cfg := shape.configure(nestedConfig(l.v.cfg.Outer.d.cfg.MaxLanes))
	cfg.Redial = l.redial
	zc, err := zmux.Start(ctx, q, zmux.RoleClient, cfg)
	if err != nil {
		l.closeDirect()
		return err
	}
	shape.attach(zc)
	l.zc, l.shape = zc, shape
	l.ctx, l.cancel = context.WithCancel(l.v.ctx)
	l.wg.Go(func() { rejectStreams(l.ctx, zc, func(wire.Op) bool { return false }) })
	l.wg.Go(l.run)
	path := "relay"
	if mode == zmux.Bound {
		path = "direct"
	}
	log.Info("p2p link up", "link", l.id, "path", path)
	return nil
}

func (l *visitorLink) openRelay(ctx context.Context) (zmux.PipeIO, error) {
	s, err := l.v.cfg.Outer.get(ctx)
	if err != nil {
		return zmux.PipeIO{}, err
	}
	info, err := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpPipe, LinkID: l.id})
	if err != nil {
		return zmux.PipeIO{}, err
	}
	p, err := s.zc.OpenPipeStream(ctx, &zmux.PipeStreamOptions{Info: info})
	if err != nil {
		return zmux.PipeIO{}, err
	}
	p.R = &refusalReader{ReadCloser: p.R, refused: &l.refused}
	return relayPipe(p), nil
}

// refusalReader notes a pipe the server refused or ended because the link
// is gone (§9.4a): no redial can succeed, so the link ends at once.
type refusalReader struct {
	io.ReadCloser
	refused *atomic.Bool
}

func (r *refusalReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	var se *zmux.StreamError
	if errors.As(err, &se) && uint64(se.ErrorCode) == wire.CodeNotAllowed {
		r.refused.Store(true)
	}
	return n, err
}

func (r *refusalReader) CarrierConn() any {
	if c, ok := r.ReadCloser.(interface{ CarrierConn() any }); ok {
		return c.CarrierConn()
	}
	return nil
}

func (r *refusalReader) SetReadDeadline(t time.Time) error {
	if d, ok := r.ReadCloser.(interface{ SetReadDeadline(time.Time) error }); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}

// punch makes a direct path and opens a pipe on it.
func (l *visitorLink) punch(ctx context.Context, background bool) (zmux.PipeIO, error) {
	s, err := l.v.cfg.Outer.get(ctx)
	if err != nil {
		return zmux.PipeIO{}, err
	}
	if s.d.cfg.Puncher == nil {
		return zmux.PipeIO{}, errors.New("no puncher")
	}
	info, err := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpPunch, LinkID: l.id, Background: background})
	if err != nil {
		return zmux.PipeIO{}, err
	}
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		return zmux.PipeIO{}, err
	}
	lc := &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
	if err := st.SetPreamble(info); err != nil || st.Flush() != nil {
		lc.abort(wire.CodeInternal)
		return zmux.PipeIO{}, errors.New("punch not opened")
	}
	path, err := s.d.cfg.Puncher.Punch(ctx, lc, background, false)
	_ = lc.Close()
	if err != nil {
		return zmux.PipeIO{}, err
	}
	p, err := zmuxquic.OpenPipe(ctx, path.QUIC)
	if err != nil {
		path.Close()
		return zmux.PipeIO{}, err
	}
	l.mu.Lock()
	old := l.direct
	l.direct = path
	l.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return p, nil
}

// closeUpPath closes the path that went down: not one punched since, which
// has not joined yet.
func (l *visitorLink) closeUpPath() {
	l.mu.Lock()
	d := l.direct
	if d == nil || d != l.upPath {
		l.mu.Unlock()
		return
	}
	l.direct, l.upPath = nil, nil
	l.mu.Unlock()
	d.Close()
}

func (l *visitorLink) closeDirect() {
	l.mu.Lock()
	d := l.direct
	l.direct, l.upPath = nil, nil
	l.upSince = time.Time{}
	l.mu.Unlock()
	if d != nil {
		d.Close()
	}
}

// redial gives zmux a pipe when the session has none in a direction: a relay,
// or in direct mode a new punch (§9.4a).
func (l *visitorLink) redial(ctx context.Context, _ zmux.Direction) (zmux.PipeIO, error) {
	if l.mode == wire.ConnDirect {
		return l.punch(ctx, false)
	}
	return l.openRelay(ctx)
}

// usable reports whether a pipe's outbound direction carries data.
func usable(p zmux.PipeStats) bool {
	return p.Outbound != zmux.OutboundNone && p.Outbound != zmux.OutboundClosed && p.Outbound != zmux.OutboundFailed
}

// isDirectPipe tells a direct QUIC pipe from a relay PIPE.
func isDirectPipe(p zmux.PipeStats) bool { return isDirectCarrier(p.Carrier) }

// isDirectCarrier is the kind of a direct path; a relay PIPE is an outer
// stream, of no kind.
func isDirectCarrier(k zmux.CarrierKind) bool { return k == zmux.CarrierQUIC }

// directActive reports whether a direct pipe is carrying data: one still
// joining or draining does not replace the relay.
func directActive(stats []zmux.PipeStats) bool {
	for _, p := range stats {
		if isDirectPipe(p) && p.Outbound == zmux.OutboundActive {
			return true
		}
	}
	return false
}

// linkOver gives up the need of l, which ended, unless a successor owns it:
// one being made (make held) or made (cur moved on).
func (v *Visitor) linkOver(l *visitorLink) {
	if !v.make.TryLock() {
		return
	}
	defer v.make.Unlock()
	v.mu.Lock()
	replaced := v.cur != nil && v.cur != l
	v.mu.Unlock()
	if !replaced {
		v.cfg.Outer.setNeed(v, false)
	}
}

// run keeps the paths (§9.4a) until the session ends.
func (l *visitorLink) run() {
	defer l.v.linkOver(l)
	defer l.shape.detach()
	defer l.closeDirect()
	defer l.cancel()
	t := time.NewTicker(l.v.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-l.zc.Context().Done():
			return
		case <-l.ctx.Done():
			return
		case <-t.C:
		}
		if l.refused.Load() {
			_ = l.zc.Close()
			return
		}
		l.step()
	}
}

func (l *visitorLink) step() {
	var relay *zmux.PipeStats
	stats := l.zc.PipeStats()
	directUp := directActive(stats)
	for _, p := range stats {
		if usable(p) && !isDirectPipe(p) {
			relay = &p
		}
	}
	if l.mode == wire.ConnRelay {
		l.v.cfg.Outer.setNeed(l.v, true)
		return
	}
	now := time.Now()
	l.mu.Lock()
	wasUp := !l.upSince.IsZero()
	if directUp && !wasUp {
		l.upSince, l.upPath = now, l.direct
		l.backoff, l.nextTry = 0, time.Time{}
	}
	if !directUp {
		l.upSince = time.Time{}
	}
	upSince, punching := l.upSince, l.punching
	l.mu.Unlock()
	if directUp && !wasUp {
		log.Info("p2p link path", "link", l.id, "path", "direct")
	}
	if !directUp && wasUp {
		log.Info("p2p link path", "link", l.id, "path", "relay")
		l.closeUpPath()
	}
	if directUp && relay != nil {
		if p, ok := l.zc.Pipe(relay.Pipe); ok {
			if !relay.Standby {
				_ = p.SetSendStandby(true)
				_ = p.SetReceiveStandby(true)
			}
			if now.Sub(upSince) >= l.v.cfg.RelayIdle && directActive(l.zc.PipeStats()) {
				_ = p.Close()
				relay = nil
			}
		}
	}
	if !directUp && l.mode == wire.ConnAuto && !punching {
		l.startPunch(now, relay != nil)
	}
	l.v.cfg.Outer.setNeed(l.v, relay != nil || punching || !directUp)
}

// startPunch tries a direct path in the background, backing off.
func (l *visitorLink) startPunch(now time.Time, background bool) {
	l.mu.Lock()
	if l.punching || now.Before(l.nextTry) {
		l.mu.Unlock()
		return
	}
	l.punching = true
	l.mu.Unlock()
	l.wg.Go(func() {
		err := l.joinDirect(background)
		l.mu.Lock()
		l.punching = false
		if err != nil {
			l.backoff = min(max(2*l.backoff, 2*time.Second), punchMaxBackoff)
			l.nextTry = time.Now().Add(l.backoff)
		}
		l.mu.Unlock()
		if err != nil && l.ctx.Err() == nil {
			log.Debug("direct path not made", "link", l.id, "err", err)
		}
	})
}

func (l *visitorLink) joinDirect(background bool) error {
	ctx, cancel := context.WithTimeout(l.ctx, 2*time.Minute)
	defer cancel()
	pipe, err := l.punch(ctx, background)
	if err != nil {
		return err
	}
	mode := zmux.Bound
	if _, err := l.zc.AddPipe(ctx, pipe, zmux.PipeOptions{Mode: &mode}); err != nil {
		l.closeDirect()
		return err
	}
	return nil
}

// ConnModeOf is the wire value of a section's conn_mode.
func ConnModeOf(mode string) uint64 {
	switch mode {
	case "relay":
		return wire.ConnRelay
	case "direct":
		return wire.ConnDirect
	}
	return wire.ConnAuto
}

// QUICConfig completes the QUIC configuration of a direct path.
func QUICConfig(base *quic.Config) *quic.Config { return zmuxquic.QUICConfig(base) }
