package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// peerWaitTimeout bounds how long a link of a direct-only section waits for
// the direct path, and the acknowledgement of a relayed link.
const peerWaitTimeout = 5 * time.Second

var errNoDirectPath = errors.New("timeout waiting for the direct path")

// peerLink is a visitor's access to one secret task (one password), shared
// by every local section with that password. It owns the paths to the
// provider — the relay through nps and, when a section wants it, the direct
// path by hole punching — and picks one for each link by the section's
// connection mode. Listeners see only OpenLink: a later single session over
// switchable carriers replaces the path choice here without touching them.
type peerLink struct {
	mgr      *SecretManager
	password string
	route    string // the first section's route, naming the link in logs
	timeout  time.Duration

	mu     sync.Mutex
	direct *directPath // nil while no section wants a direct path
	via    string      // the path the last link took: direct, relay or ""
}

func newPeerLink(mgr *SecretManager, password, route string) *peerLink {
	return &peerLink{mgr: mgr, password: password, route: route, timeout: peerWaitTimeout}
}

// wantDirect starts the direct path on first use; relayOK records that some
// section relays while it is down, which lets it punch in the background.
func (p *peerLink) wantDirect(relayOK bool) {
	p.mu.Lock()
	d := p.direct
	if d == nil {
		d = newDirectPath(p)
		p.direct = d
		if p.mgr.ctx.Err() == nil {
			p.mgr.wg.Go(d.run)
		}
	}
	p.mu.Unlock()
	if relayOK {
		d.relay.Store(true)
	}
}

func (p *peerLink) directPath() *directPath {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.direct
}

// OpenLink opens a connection to the task for link in mode:
//   - relay: through nps;
//   - auto: over the direct path when it is up, else, or when opening a
//     stream on it fails, through nps; links go direct again once it is;
//   - direct: over the direct path only, waiting up to the timeout for it.
//
// A link whose target nps reaches itself (local proxy) is always relayed.
// A direct path that fails a link takes no new links while it is checked.
func (p *peerLink) OpenLink(ctx context.Context, link *conn.Link, mode string) (net.Conn, error) {
	if link == nil {
		return nil, errors.New("link is nil")
	}
	d := p.directPath()
	if mode == config.ConnRelay || d == nil || link.LocalProxy {
		if mode == config.ConnDirect {
			return nil, errDirectLocalProxy
		}
		return p.viaRelay(*link, "")
	}
	t := d.active()
	if t == nil && mode == config.ConnDirect {
		wctx, cancel := context.WithTimeout(ctx, p.timeout)
		t = d.waitActive(wctx)
		cancel()
		if t == nil {
			return nil, errNoDirectPath
		}
	}
	var directErr error
	if t != nil {
		c, err := t.openLink(ctx, *link)
		if err == nil {
			p.used("direct", "direct_up", nil)
			p2pLog.Log(context.Background(), slogx.LevelTrace, "link opened", "route", p.route, "via", "direct", "target", link.Host)
			return c, nil
		}
		d.suspect(t, err)
		if mode == config.ConnDirect {
			return nil, fmt.Errorf("direct path: %w", err)
		}
		directErr = err
	}
	reason := "direct_down"
	if directErr != nil {
		reason = "direct_failed"
	}
	c, err := p.viaRelay(*link, reason)
	if err != nil && directErr != nil {
		err = fmt.Errorf("%w (direct path: %v)", err, directErr)
	}
	return c, err
}

func (p *peerLink) viaRelay(link conn.Link, reason string) (net.Conn, error) {
	c, err := p.mgr.relay(p.password, link, p.timeout)
	if err == nil && reason != "" {
		p.used("relay", reason, nil)
	}
	return c, err
}

// used records the path a link took and logs a change of path.
func (p *peerLink) used(via, reason string, err error) {
	p.mu.Lock()
	prev := p.via
	p.via = via
	p.mu.Unlock()
	if prev == via || prev == "" && via == "direct" {
		return
	}
	args := []any{"route", p.route, "via", via, "reason", reason}
	if tun, peer := p.ids(); tun != 0 {
		args = append(args, "tunnel", tun, "peer", peer)
	}
	if err != nil {
		args = append(args, "err", err)
	}
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, args...)
}

// ids are the task and provider client ids nps last named, 0 before.
func (p *peerLink) ids() (tunnel, peer int) {
	if d := p.directPath(); d != nil {
		return d.ids()
	}
	return 0, 0
}

// directLost is called by the direct path when it goes down: links that
// relay meanwhile log the change when they are opened.
func (p *peerLink) directLost(reason string, err error) {
	p.mu.Lock()
	wasDirect := p.via == "direct"
	p.mu.Unlock()
	if !wasDirect {
		return
	}
	d := p.directPath()
	if d != nil && d.relay.Load() {
		p.used("relay", reason, err)
		return
	}
	p.mu.Lock()
	p.via = ""
	p.mu.Unlock()
	args := []any{"route", p.route, "reason", reason, "err", err}
	if tun, peer := p.ids(); tun != 0 {
		args = append(args, "tunnel", tun, "peer", peer)
	}
	slogx.Emit(context.Background(), p2pLog, &logevent.P2PFailed, args...)
}
