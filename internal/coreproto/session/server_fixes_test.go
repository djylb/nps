package session

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

func visitorSession(t *testing.T, p *peerEnv) (context.Context, *clientSession) {
	t.Helper()
	ctx := peerCtx(t)
	s, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s
}

func TestCredentialLossRevokesLinks(t *testing.T) {
	for name, drop := range map[string]func(a *Acceptor){
		"visitor disabled": func(a *Acceptor) { a.ClientChanged(coreapi.ClientRecord{ID: 2, VKey: "vis"}) },
		"visitor rekeyed":  func(a *Acceptor) { a.ClientChanged(coreapi.ClientRecord{ID: 2, VKey: "vis2", Enabled: true}) },
		"visitor removed":  func(a *Acceptor) { a.ClientRemoved(2) },
		"visitor kicked":   func(a *Acceptor) { a.Kick(2, true) },
		"provider removed": func(a *Acceptor) { a.ClientRemoved(1) },
	} {
		t.Run(name, func(t *testing.T) {
			p := startPeer(t, false)
			ctx, vis := visitorSession(t, p)
			r := linkReply(t, ctx, vis)
			drop(p.a)
			waitFor(t, "link revoked", func() bool { return p.a.linkOf(r.LinkID) == nil })
		})
	}
}

// §9.1: the visitor going offline leaves the link to the provider's anchor.
func TestVisitorOfflineKeepsLink(t *testing.T) {
	p := startPeer(t, false)
	ctx, vis := visitorSession(t, p)
	r := linkReply(t, ctx, vis)
	_ = vis.Close()
	<-vis.Done()
	time.Sleep(100 * time.Millisecond)
	if p.a.linkOf(r.LinkID) == nil {
		t.Fatal("an offline visitor lost its link")
	}
}

func openExit(t *testing.T, ctx context.Context, s *clientSession, id uint64) *zmux.Stream {
	t.Helper()
	info, err := wire.AppendStreamOpen(&wire.StreamOpen{Op: wire.OpTCP, WantReply: true, ProviderTarget: true, LinkID: id})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(info)
	_ = st.Flush()
	return st
}

func TestRevokeAbortsServerExitStreams(t *testing.T) {
	p := startPeer(t, true)
	ctx, vis := visitorSession(t, p)
	st := openExit(t, ctx, vis, linkReply(t, ctx, vis).LinkID)
	_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
	if rep, err := wire.ReadReply(st, replyLimit); err != nil || rep.Code != wire.CodeOK {
		t.Fatal(rep, err)
	}
	p.a.RevokeLinks(func(_, _, _ int) bool { return true })
	wantReset(t, st, wire.CodeNotAllowed)
}

func TestExitLinkEndedRefusesStream(t *testing.T) {
	l := &peerLink{exits: map[*linkConn]struct{}{}, paths: map[wire.Op]*linkPath{}, grant: &coreapi.LinkGrant{}}
	l.end(wire.CodeOK)
	if l.addExit(&linkConn{}) {
		t.Fatal("an ended link took a stream")
	}
}

// §4.4: the reply comes from the outcome of the dial.
func TestExitFailureIsTheReply(t *testing.T) {
	p := startPeer(t, true)
	p.exitErr = linkError(wire.CodeConnectionRefused)
	ctx, vis := visitorSession(t, p)
	st := openExit(t, ctx, vis, linkReply(t, ctx, vis).LinkID)
	_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
	rep, err := wire.ReadReply(st, replyLimit)
	if err != nil || rep.Code != wire.CodeConnectionRefused {
		t.Fatalf("reply %+v, err %v", rep, err)
	}
}

func TestPipeRefusedByAdmission(t *testing.T) {
	p := startPeer(t, false)
	p.admit = func(func()) (func(), error) { return nil, errors.New("full") }
	ctx, vis := visitorSession(t, p)
	wantPipeRefused(t, ctx, vis, linkReply(t, ctx, vis).LinkID)
}

type countLimiter struct{ n *atomic.Int64 }

func (l countLimiter) Ready(_ context.Context, _, m int) (int, error) { return m, nil }
func (l countLimiter) TryReady(m int) int                             { return m }
func (l countLimiter) Charge(n int)                                   { l.n.Add(int64(n)) }

// A PIPE holds its slot while it runs and is metered as bytes pass.
func TestPipeHoldsSlotAndMeters(t *testing.T) {
	p := startPeer(t, false)
	var admitted, released, up, down atomic.Int64
	p.admit = func(func()) (func(), error) {
		admitted.Add(1)
		return func() { released.Add(1) }, nil
	}
	p.limiters = func() (coreapi.Limiter, coreapi.Limiter) { return countLimiter{&up}, countLimiter{&down} }
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	echoOnce(t, peerCtx(t), v, "metered")
	if admitted.Load() == 0 || released.Load() != 0 {
		t.Fatalf("admitted %d released %d while the pipe runs", admitted.Load(), released.Load())
	}
	if up.Load() == 0 || down.Load() == 0 {
		t.Fatalf("up %d down %d: not metered while running", up.Load(), down.Load())
	}
	_ = v.Close()
	waitFor(t, "slot released", func() bool { return released.Load() == admitted.Load() })
}

func TestPipeStopEndsPipe(t *testing.T) {
	p := startPeer(t, false)
	stopped := make(chan func(), 1)
	var released atomic.Int64
	p.admit = func(stop func()) (func(), error) {
		select {
		case stopped <- stop:
		default:
		}
		return func() { released.Add(1) }, nil
	}
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	echoOnce(t, peerCtx(t), v, "x")
	(<-stopped)()
	waitFor(t, "expired pipe ended", func() bool { return released.Load() > 0 })
}

func TestAccessOnTheWire(t *testing.T) {
	open := func(t *testing.T, ctx context.Context, s *clientSession, access []byte) uint64 {
		info, _ := wire.AppendLinkOpen(&wire.LinkOpen{Access: access})
		st, err := s.zc.OpenStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.SetPreamble(info)
		_ = st.Flush()
		_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
		rep, err := wire.ReadReply(st, replyLimit)
		if err != nil {
			t.Fatal(err)
		}
		return rep.Code
	}
	p := startPeer(t, false)
	ctx, vis := visitorSession(t, p)
	if c := open(t, ctx, vis, []byte("secret")); c != wire.CodeMalformed {
		t.Fatalf("plain password: code %#x", c)
	}
	if c := open(t, ctx, vis, wire.AppendAccess([]byte("secret"), make([]byte, 32))); c != wire.CodeNotAllowed {
		t.Fatalf("proof of another session: code %#x", c)
	}
}

func TestProviderWithoutCapGetsNoLink(t *testing.T) {
	p := startPeer(t, false)
	if p.a.providerOf(1) == nil {
		t.Fatal("no provider")
	}
	// The ticket is shared with the running session: swap in a session of
	// its own.
	live := p.a.sessionsOf(1)[0]
	t2 := *live.t
	t2.hello.Caps = nil
	p.a.mu.Lock()
	p.a.sess[1] = []*serverSession{{a: p.a, zc: live.zc, t: &t2}}
	p.a.mu.Unlock()
	if p.a.providerOf(1) != nil {
		t.Fatal("a session without cap link is a provider")
	}
}

// The key changed after Commit, unseen by a ClientChanged yet.
func TestRekeyBetweenCommitAndRegister(t *testing.T) {
	var e *env
	e = startHooks(t, coreapi.Hooks{Online: func(coreapi.Session) { e.a.table.set(rec(1, "key-new", true)) }}, rec(1, "key-old", true))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The handshake may or may not outlive the refusal.
	if s, err := e.dialer("key-old").Dial(ctx); err == nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	e.waitOnline()
	select {
	case r := <-e.offline:
		if r != "deleted" {
			t.Fatalf("reason %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session under the old key stays")
	}
}

func TestPathAddAfterStopAborts(t *testing.T) {
	e := start(t, "tcp", rec(1, "k", true))
	e.dial(e.dialer("k"))
	s := e.waitOnline()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := &linkPath{abort: wire.CodeNotAllowed}
	p.stop()
	lc := s.conn(st)
	if p.add(lc) {
		t.Fatal("a stopped path took an end")
	}
	if _, err := lc.Write([]byte("x")); err == nil {
		t.Fatal("end of a stopped path still open")
	}
}

type stallConn struct {
	net.Conn
	addr   net.Addr
	closed atomic.Bool
}

func (c *stallConn) RemoteAddr() net.Addr { return c.addr }
func (c *stallConn) Close() error         { c.closed.Store(true); return nil }

func newStallConn(ip string, port int) *stallConn {
	return &stallConn{addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: port}}
}

func TestStallLimits(t *testing.T) {
	for name, cfg := range map[string]struct {
		total, source int
		second        *stallConn
	}{
		"global":          {1, 8, newStallConn("10.0.0.2", 1)},
		"per source":      {8, 1, newStallConn("10.0.0.1", 2)},
		"per ipv6 prefix": {8, 1, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e := start(t, "tcp", rec(1, "k", true))
			e.a.stallMax, e.a.stallPerSource = cfg.total, cfg.source
			first, second := newStallConn("10.0.0.1", 1), cfg.second
			if second == nil {
				first, second = newStallConn("2001:db8::1", 1), newStallConn("2001:db8::2", 1)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { e.a.Stall(ctx, first); close(done) }()
			t.Cleanup(func() { cancel(); <-done })
			waitFor(t, "first stalled", func() bool {
				e.a.stallMu.Lock()
				defer e.a.stallMu.Unlock()
				return e.a.stallTotal == 1
			})
			e.a.Stall(ctx, second)
			if !second.closed.Load() {
				t.Fatal("over the limit and not closed")
			}
			if first.closed.Load() {
				t.Fatal("the stalled carrier was closed")
			}
		})
	}
}
