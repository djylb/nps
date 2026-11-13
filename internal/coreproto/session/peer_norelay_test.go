package session

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

// A tunnel that does not allow relaying: the LINK is answered with NoRelay
// and the direct mode, and its PIPE is refused while PUNCH still works.
func TestPeerNoRelayRefusesPipe(t *testing.T) {
	p := startPeer(t, false)
	p.noRelay.Store(true)
	ctx := peerCtx(t)
	vis, err := p.outer.get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReplyMode(t, ctx, vis, wire.ConnAuto)
	if !r.NoRelay || r.ConnMode != wire.ConnDirect {
		t.Fatalf("reply %+v", r)
	}
	wantPipeRefused(t, ctx, vis, r.LinkID)
	if p.a.linkOf(r.LinkID) == nil {
		t.Fatal("refused PIPE ended the link")
	}
}

// Auto against a tunnel without relay goes direct only.
func TestPeerNoRelayAutoGoesDirect(t *testing.T) {
	p := startPeer(t, false)
	p.noRelay.Store(true)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	echoOnce(t, peerCtx(t), v, "direct")
	waitPipes(t, v, "direct only", func(d, r bool) bool { return d && !r })
	if p.punch.punch.Load() == 0 {
		t.Fatal("no punch")
	}
}

// Auto whose punch fails reports it instead of falling back to a relay.
func TestPeerNoRelayAutoPunchFails(t *testing.T) {
	p := startPeer(t, false)
	p.noRelay.Store(true)
	p.punch.fail.Store(true)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	_, err := v.OpenLink(peerCtx(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err == nil || !strings.Contains(err.Error(), "does not relay") {
		t.Fatalf("err %v", err)
	}
}

// A relay-only visitor is refused at LINK with the reason.
func TestPeerNoRelayRelayModeRefused(t *testing.T) {
	p := startPeer(t, false)
	p.noRelay.Store(true)
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	_, err := v.OpenLink(peerCtx(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err == nil || !coreapi.IsNotAllowed(err) || !strings.Contains(err.Error(), RelayDisabledReason) {
		t.Fatalf("err %v", err)
	}
	if p.punch.punch.Load() != 0 {
		t.Fatal("relay mode punched")
	}
}

// A spent traffic quota stops the relay only: the relay PIPE is ended, the
// next LINK says NoRelay, and the direct path carries the access.
func TestPeerRelayQuotaStopsRelayOnly(t *testing.T) {
	p := startPeer(t, false)
	var ok atomic.Bool
	ok.Store(true)
	lim := quotaLimiter{&ok}
	p.limiters = func() (coreapi.Limiter, coreapi.Limiter) { return lim, lim }
	p.punch.fail.Store(true)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	checkEcho(t, c, "relay")
	l := v.live()
	ok.Store(false)
	p.noRelay.Store(true) // the quota is what RelayAllowed reads
	_, _ = c.Write([]byte("over"))
	waitUntil(t, "relay stopped", func() bool { return l.ended() })
	if p.a.linkOf(l.id) != nil {
		wantPipeRefused(t, ctx, mustOuter(t, ctx, p), l.id)
	}
	p.punch.fail.Store(false)
	echoOnce(t, ctx, v, "direct")
	waitPipes(t, v, "direct only", func(d, r bool) bool { return d && !r })
}

// A relay refused while the direct path is active leaves the link up on
// the direct path, which no longer redials a relay.
func TestPeerRelayRefusedKeepsDirect(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	checkEcho(t, c, "up")
	waitPipes(t, v, "direct", func(d, _ bool) bool { return d })
	l := v.live()
	if l == nil {
		t.Fatal("no live link")
	}
	p.noRelay.Store(true)
	l.refused.Store(true)
	waitUntil(t, "relay off", func() bool { return l.relayOff.Load() })
	if l.ended() {
		t.Fatal("link ended")
	}
	wantPipeRefused(t, ctx, mustOuter(t, ctx, p), l.id)
	checkEcho(t, c, "still direct")
}

func mustOuter(t *testing.T, ctx context.Context, p *peerEnv) *clientSession {
	t.Helper()
	s, err := p.outer.get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Relay stopped by a quota comes back when the limit no longer blocks: the
// visitor is told on its LINK stream and relays again without a new link.
func TestPeerRelayComesBack(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	checkEcho(t, c, "up")
	waitPipes(t, v, "direct", func(d, _ bool) bool { return d })
	l := v.live()
	waitUntil(t, "watching", func() bool { return p.relayWatch.Load() != nil })
	p.setNoRelay(true)
	waitUntil(t, "told relay off", func() bool { return l.relayOff.Load() })
	wantPipeRefused(t, ctx, mustOuter(t, ctx, p), l.id)
	checkEcho(t, c, "direct")

	p.setNoRelay(false)
	waitUntil(t, "told relay on", func() bool { return !l.relayOff.Load() })
	p.punch.fail.Store(true)
	l.closeDirect()
	waitPipes(t, v, "relay again", func(_, r bool) bool { return r })
	checkEcho(t, c, "relay")
	if v.live() != l {
		t.Fatal("link was made again")
	}
}
