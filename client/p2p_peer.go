package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/quicpath"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
	"github.com/djylb/timeshim"
)

var p2pLog = slogx.Module("p2p")

// p2pStableAfter is how long a direct path must have lived for its loss to
// be retried at once; a path lost sooner counts as a failed attempt.
const p2pStableAfter = time.Minute

var errP2PPathLost = errors.New("direct path lost soon after it was established")

// p2pPeer is the direct path of the visitor to one P2P task (one password):
// every local section with that password shares it. Its goroutine punches,
// holds the QUIC transport while it lives and punches again with a backoff
// when it ends.
type p2pPeer struct {
	mgr      *P2PManager
	password string
	label    string
	relay    atomic.Bool // some section relays when the direct path is down

	// demand wakes a peer that gave up punching: a connection waits for
	// the direct path.
	demand chan struct{}

	mu        sync.Mutex
	transport *p2pTransport
	changed   chan struct{} // closed and replaced whenever a transport is installed
}

func newP2PPeer(mgr *P2PManager, password, label string) *p2pPeer {
	return &p2pPeer{mgr: mgr, password: password, label: label, changed: make(chan struct{}), demand: make(chan struct{}, 1)}
}

// active returns the live direct transport that is not under suspicion, or
// nil.
func (p *p2pPeer) active() *p2pTransport {
	p.mu.Lock()
	t := p.transport
	p.mu.Unlock()
	if t != nil && t.Alive() && !t.suspect.Load() {
		return t
	}
	return nil
}

// waitActive waits for a live direct transport until ctx ends.
func (p *p2pPeer) waitActive(ctx context.Context) *p2pTransport {
	for {
		p.mu.Lock()
		t, changed := p.transport, p.changed
		p.mu.Unlock()
		if t != nil && t.Alive() && !t.suspect.Load() {
			return t
		}
		select {
		case p.demand <- struct{}{}:
		default:
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil
		}
	}
}

func (p *p2pPeer) install(t *p2pTransport) {
	p.mu.Lock()
	p.transport = t
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

func (p *p2pPeer) uninstall(t *p2pTransport) {
	p.mu.Lock()
	if p.transport == t {
		p.transport = nil
	}
	p.mu.Unlock()
}

// suspect takes a transport that failed a connection out of use for new
// connections and checks it: a slow moment must not cost the streams it
// carries, a dead path is closed. A transport that passes the check is used
// again.
func (p *p2pPeer) suspect(t *p2pTransport, err error) {
	if p.mgr.ctx.Err() != nil || !t.suspect.CompareAndSwap(false, true) {
		return
	}
	p2pLog.Info("direct path failed a connection, checking it", "peer", p.label, "addr", t.QUIC().RemoteAddr().String(), "err", err)
	p.mgr.wg.Go(func() {
		var last error
		for range p2pCheckAttempts {
			if last = t.check(p.mgr.ctx); last == nil {
				t.suspect.Store(false)
				p.mu.Lock()
				close(p.changed)
				p.changed = make(chan struct{})
				p.mu.Unlock()
				slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, "peer", p.label, "path", "direct", "addr", t.QUIC().RemoteAddr().String(), "reason", "check_passed")
				return
			}
			if !t.Alive() || p.mgr.ctx.Err() != nil {
				return
			}
		}
		p.lost(t, last)
	})
}

// lost takes a dead transport out of use and closes it, which wakes the
// goroutine holding it to punch again.
func (p *p2pPeer) lost(t *p2pTransport, err error) {
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, "peer", p.label, "path", "relay", "addr", t.QUIC().RemoteAddr().String(), "reason", "check_failed", "err", err)
	p.uninstall(t)
	_ = t.Close()
}

// hold serves t until its QUIC connection ends or the manager closes, and
// returns how long it lived.
func (p *p2pPeer) hold(t *p2pTransport) time.Duration {
	start := time.Now()
	p.install(t)
	select {
	case <-t.Done():
		err := t.Err()
		if errors.Is(err, quicpath.ErrIdleTimeout) {
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, "peer", p.label, "path", "relay", "addr", t.QUIC().RemoteAddr().String(), "reason", "idle_timeout", "err", err)
		} else {
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, "peer", p.label, "path", "relay", "addr", t.QUIC().RemoteAddr().String(), "reason", "closed", "err", err)
		}
	case <-p.mgr.ctx.Done():
	}
	p.uninstall(t)
	_ = t.Close()
	return time.Since(start)
}

// run punches and holds direct paths until the manager closes. After a
// failure it follows holepunch.NextAttempt; once that gives up it punches
// again only when a connection waits for the direct path.
func (p *p2pPeer) run() {
	ctx := p.mgr.ctx
	var b p2pBackoff
	var wait time.Duration
	background, gaveUp := false, false
	for {
		if gaveUp {
			select {
			case <-p.demand:
			case <-ctx.Done():
				return
			}
			gaveUp = false
		} else if timeshim.Sleep(ctx, timeshim.Real, wait) != nil {
			return
		}
		t, err := p.punch(ctx, background)
		if ctx.Err() != nil {
			if t != nil {
				_ = t.Close()
			}
			return
		}
		if err != nil {
			a := b.failed(err)
			wait = a.Delay
			background = a.Action == holepunch.ActionBackground && p.relay.Load()
			if a.Action == holepunch.ActionGiveUp {
				// Demand from before this attempt does not count.
				select {
				case <-p.demand:
				default:
				}
				gaveUp = true
				slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, slogx.KeyMsg, "next attempt when a connection waits for it", "peer", p.label, "reason", "punch_failed", "err", err)
				continue
			}
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, "peer", p.label, "reason", "punch_failed", "err", err, "next_mode", p2pModeName(background), "next_in_ms", wait.Milliseconds())
			continue
		}
		background = false
		if p.hold(t) >= p2pStableAfter {
			b.reset()
			wait = 0
		} else {
			wait = b.failed(errP2PPathLost).Delay
		}
	}
}

// punch runs one hole-punching session through the bridge and connects QUIC
// over the punched path.
func (p *p2pPeer) punch(ctx context.Context, background bool) (*p2pTransport, error) {
	mgr := p.mgr
	rc, server, err := mgr.openTunnelConn()
	if err != nil {
		return nil, err
	}
	// openP2PControl owns the stream and closes it on failure.
	//noinspection GoResourceLeak
	tc, sessionID, err := openP2PControl(ctx, conn.NewConn(rc), legacy.WorkP2P, mgr.getUUID(), server.fingerprint,
		legacy.P2PRequest{Password: crypt.Md5(p.password), Background: background, ServerIP: server.ip})
	if err != nil {
		return nil, err
	}
	p2pLog.Debug("punching", "peer", p.label, "session", sessionID, "mode", p2pModeName(background))
	// tc is closed right below; on success the session is handed to the transport.
	//noinspection GoResourceLeak
	sess, err := holepunch.Punch(ctx, tc, p2pOptions(mgr.cfg.LocalIP, mgr.mapper, background, p.label))
	_ = tc.Close()
	if err != nil {
		var herr *holepunch.Error
		if errors.As(err, &herr) {
			p2pLog.Debug("punch failed", "peer", p.label, "session", sessionID, "nat", p2pNATSummary(herr.Diagnostics))
		}
		return nil, err
	}
	diag := sess.Diagnostics()
	p2pLog.Debug("punched", "peer", p.label, "session", sessionID, "nat", p2pNATSummary(&diag))
	t, err := dialP2PTransport(ctx, sess)
	if err != nil {
		return nil, err
	}
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PConnected, "peer", p.label, "path", "direct", "addr", t.QUIC().RemoteAddr().String(), "session", sessionID)
	return t, nil
}

func p2pModeName(background bool) string {
	if background {
		return "background"
	}
	return "interactive"
}
