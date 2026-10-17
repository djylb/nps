package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/p2pproto"
)

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

	mu        sync.Mutex
	transport *p2pTransport
	changed   chan struct{} // closed and replaced whenever a transport is installed
}

func newP2PPeer(mgr *P2PManager, password, label string) *p2pPeer {
	return &p2pPeer{mgr: mgr, password: password, label: label, changed: make(chan struct{})}
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
	logs.Info("%s: direct path to %v failed a connection (%v), checking it", p.label, t.qc.RemoteAddr(), err)
	p.mgr.wg.Go(func() {
		var last error
		for range p2pCheckAttempts {
			if last = t.check(p.mgr.ctx); last == nil {
				t.suspect.Store(false)
				p.mu.Lock()
				close(p.changed)
				p.changed = make(chan struct{})
				p.mu.Unlock()
				logs.Info("%s: direct path to %v answers again", p.label, t.qc.RemoteAddr())
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
	logs.Warn("%s: direct path to %v failed: %v", p.label, t.qc.RemoteAddr(), err)
	p.uninstall(t)
	t.Close(p2pCodeLost, "lost")
}

// hold serves t until its QUIC connection ends or the manager closes, and
// returns how long it lived.
func (p *p2pPeer) hold(t *p2pTransport) time.Duration {
	start := time.Now()
	p.install(t)
	select {
	case <-t.Done():
		logs.Warn("%s: direct path to %v closed: %v", p.label, t.qc.RemoteAddr(), context.Cause(t.qc.Context()))
	case <-p.mgr.ctx.Done():
	}
	p.uninstall(t)
	t.Close(p2pCodeClosed, "closed")
	return time.Since(start)
}

// run punches and holds direct paths until the manager closes.
func (p *p2pPeer) run() {
	ctx := p.mgr.ctx
	var b p2pBackoff
	var wait time.Duration
	background := false
	for {
		if !sleepWithContext(ctx, wait) {
			return
		}
		t, err := p.punch(ctx, background)
		if ctx.Err() != nil {
			if t != nil {
				t.Close(p2pCodeClosed, "closed")
			}
			return
		}
		if err != nil {
			wait, background = b.failed(err, p.relay.Load(), background)
			logs.Warn("%s: no direct path: %v; next %s attempt in %s", p.label, err, p2pModeName(background), wait.Round(time.Second))
			continue
		}
		background = false
		if p.hold(t) >= p2pStableAfter {
			b.reset()
			wait = 0
		} else {
			wait, _ = b.failed(errP2PPathLost, false, false)
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
	tc, sessionID, err := openP2PControl(ctx, conn.NewConn(rc), common.WORK_P2P, mgr.getUUID(), server.fingerprint,
		p2pproto.Request{Password: crypt.Md5(p.password), Background: background, ServerIP: server.ip})
	if err != nil {
		return nil, err
	}
	var mode protocol.SessionMode
	if background {
		mode = protocol.ModeBackground
	}
	logs.Debug("%s: punching in session %s (%s)", p.label, sessionID, p2pModeName(background))
	sess, err := holepunch.AttachSession(ctx, tc, p2pOptions(mgr.cfg.LocalIP, mgr.mapper, mode))
	_ = tc.Close()
	if err != nil {
		var herr *holepunch.Error
		if errors.As(err, &herr) {
			logs.Debug("%s: session %s failed, local NAT %s", p.label, sessionID, p2pNATSummary(herr.Diagnostics))
		}
		return nil, err
	}
	diag := sess.Diagnostics()
	logs.Info("%s: punched %s; local NAT %s", p.label, p2pResultSummary(sess), p2pNATSummary(&diag))
	t, err := dialP2PTransport(ctx, sess)
	if err != nil {
		return nil, err
	}
	logs.Info("%s: direct QUIC connection to %v established", p.label, t.qc.RemoteAddr())
	return t, nil
}

func p2pModeName(background bool) string {
	if background {
		return "background"
	}
	return "interactive"
}
