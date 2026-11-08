package session

import (
	"context"
	"errors"
	"net"
	"sync"
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
	// Keep holds the session open whatever the visitors need
	// (p2p_visitor_keep_server).
	Keep bool
	// Hosting reports tunnels or virtual services this npc serves through
	// the session; nil is none.
	Hosting func() bool
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

func (o *OuterSessions) live() *clientSession {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur != nil && o.cur.zc.Context().Err() == nil {
		return o.cur
	}
	o.cur = nil
	return nil
}

// Get returns the session, dialing it if there is none.
func (o *OuterSessions) Get(ctx context.Context) (*clientSession, error) {
	if s := o.live(); s != nil {
		return s, nil
	}
	o.dial.Lock()
	defer o.dial.Unlock()
	if s := o.live(); s != nil {
		return s, nil
	}
	cs, err := o.d.Dial(ctx)
	if err != nil {
		return nil, err
	}
	s := cs.(*clientSession)
	o.mu.Lock()
	o.cur = s
	o.mu.Unlock()
	return s, nil
}

// Dial implements coreapi.Dialer with the session itself: a host keeps the
// session through it, and the visitors that share it keep theirs.
func (o *OuterSessions) Dial(ctx context.Context) (coreapi.ClientSession, error) {
	s, err := o.Get(ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Server implements coreapi.Dialer.
func (o *OuterSessions) Server() string { return o.d.Server() }

// Visitor implements coreapi.Outer.
func (o *OuterSessions) Visitor(access []byte, mode string, relayIdle time.Duration) coreapi.Visitor {
	return NewVisitor(VisitorConfig{Outer: o, Access: access, Mode: ConnModeOf(mode), RelayIdle: relayIdle})
}

// Hold implements coreapi.Outer. It is called before the session is used.
func (o *OuterSessions) Hold(keep bool, hosting func() bool) {
	o.mu.Lock()
	o.Keep, o.Hosting = keep, hosting
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
	Outer     *OuterSessions
	Access    []byte
	Mode      uint64        // wire.ConnRelay, ConnAuto or ConnDirect
	RelayIdle time.Duration // 0 is DefaultRelayIdle
	Tick      time.Duration // 0 is one second; tests
	Timeout   time.Duration // 0 is 15 s: making the link
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
	return openLink(ctx, l.zc, r, nil, v.cfg.Outer.d.limits)
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

	zc     *zmux.Conn
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	punching bool
	direct   *coreapi.DirectPath
	upSince  time.Time // the direct path up, continuously
	backoff  time.Duration
	nextTry  time.Time
}

func (l *visitorLink) ended() bool {
	if l.exit != nil {
		return l.exit.Context().Err() != nil
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
	s, err := v.cfg.Outer.Get(ctx)
	if err != nil {
		return nil, err
	}
	info, err := wire.AppendLinkOpen(&wire.LinkOpen{Access: v.cfg.Access, ConnMode: v.cfg.Mode})
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
	q, err := seal.Establish(ctx, pipe, &seal.ClientConfig{Anchor: &anchor{}, Mode: mode, Handle: handle, PSK: psk})
	if err != nil {
		l.closeDirect()
		return err
	}
	cfg := nestedConfig(l.v.cfg.Outer.d.cfg.MaxLanes)
	cfg.Redial = l.redial
	zc, err := zmux.Start(ctx, q, zmux.RoleClient, cfg)
	if err != nil {
		l.closeDirect()
		return err
	}
	l.zc = zc
	l.ctx, l.cancel = context.WithCancel(l.v.ctx)
	l.wg.Go(func() { rejectStreams(l.ctx, zc, func(wire.Op) bool { return false }) })
	l.wg.Go(l.run)
	return nil
}

func (l *visitorLink) openRelay(ctx context.Context) (zmux.PipeIO, error) {
	s, err := l.v.cfg.Outer.Get(ctx)
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
	return relayPipe(p), nil
}

// punch makes a direct path and opens a pipe on it.
func (l *visitorLink) punch(ctx context.Context, background bool) (zmux.PipeIO, error) {
	s, err := l.v.cfg.Outer.Get(ctx)
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

func (l *visitorLink) closeDirect() {
	l.mu.Lock()
	d := l.direct
	l.direct = nil
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
func isDirectPipe(p zmux.PipeStats) bool { return p.Carrier == zmux.CarrierQUIC }

// run keeps the paths (§9.4a) until the session ends.
func (l *visitorLink) run() {
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
		l.step()
	}
}

func (l *visitorLink) step() {
	var relay *zmux.PipeStats
	directUp := false
	for _, p := range l.zc.PipeStats() {
		if !usable(p) {
			continue
		}
		if isDirectPipe(p) {
			directUp = true
		} else {
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
		l.upSince = now
		l.backoff, l.nextTry = 0, time.Time{}
	}
	if !directUp {
		l.upSince = time.Time{}
	}
	upSince, punching, direct := l.upSince, l.punching, l.direct
	l.mu.Unlock()
	if !directUp && wasUp && direct != nil {
		l.closeDirect()
	}
	if directUp && relay != nil {
		if p, ok := l.zc.Pipe(relay.Pipe); ok {
			if !relay.Standby {
				_ = p.SetSendStandby(true)
				_ = p.SetReceiveStandby(true)
			}
			if now.Sub(upSince) >= l.v.cfg.RelayIdle {
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
