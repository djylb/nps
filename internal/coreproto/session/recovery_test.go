package session

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/timeshim"
	"github.com/zmuxio/zmux-go"
)

func fakeRedial(jitter float64) (*clientSession, *timeshim.Fake) {
	c := timeshim.NewFake(time.Unix(1000, 0))
	d := NewDialer(DialConfig{})
	d.clock, d.jitter = c, func() float64 { return jitter }
	return &clientSession{d: d}, c
}

// The first redial of a suspension waits in [0, 1s), each failure doubles
// the window up to a minute, and a resume starts over.
func TestRedialWaitFullJitter(t *testing.T) {
	s, _ := fakeRedial(0.5)
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if d := s.redialWait(); d != w {
			t.Fatalf("redial %d waits %v, want %v", i, d, w)
		}
	}
	s.redialDone()
	if d := s.redialWait(); d != 500*time.Millisecond {
		t.Fatalf("after a resume the redial waits %v, want 500ms", d)
	}
	for _, j := range []float64{0, 0.999999} {
		s, _ := fakeRedial(j)
		if d := s.redialWait(); d < 0 || d >= redialFirst {
			t.Fatalf("jitter %v: first wait %v not in [0, %v)", j, d, redialFirst)
		}
	}
	if s.d.sessionConfig(&clientSession{d: s.d, plan: nil}).RedialBackoff != noRedialBackoff {
		t.Fatal("zmux paces the redials with a backoff of its own")
	}
}

// redial waits on the Dialer's clock before it dials.
func TestRedialWaitsOnClock(t *testing.T) {
	s, c := fakeRedial(0.5)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.redial(ctx, 0)
		done <- err
	}()
	if err := c.WaitPending(ctx, 1); err != nil {
		t.Fatal(err)
	}
	c.Advance(499 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("redial returned before its wait: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	c.Advance(time.Millisecond)
	if err := <-done; err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("redial without candidates: %v", err)
	}

	// The wait ends with the context.
	cctx, ccancel := context.WithCancel(context.Background())
	go func() {
		_, err := s.redial(cctx, 0)
		done <- err
	}()
	if err := c.WaitPending(ctx, 1); err != nil {
		t.Fatal(err)
	}
	ccancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("redial after cancel: %v", err)
	}
}

// The gate blocks once full, lets the next in on a release and gives up a
// wait when its context ends.
func TestRecoveryGate(t *testing.T) {
	g := newRecoveryGate(2)
	ctx := context.Background()
	r1, err := g.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		r, err := g.acquire(ctx)
		if err == nil {
			r()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("a full gate let a third in: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	r1()
	if err := <-got; err != nil {
		t.Fatal(err)
	}

	// Full again: a waiter leaves with its context.
	if _, err := g.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		_, err := g.acquire(cctx)
		got <- err
	}()
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("a wait outlived its context: %v", err)
	}

	// recoveryDial holds the gate across dialCarrier.
	d := NewDialer(DialConfig{})
	d.recovery = newRecoveryGate(1)
	release, _ := d.recovery.acquire(ctx)
	tctx, tcancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer tcancel()
	if _, err := d.recoveryDial(tctx, connprofile.Candidate{Carrier: "tcp", Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("recoveryDial passed a full gate")
	}
	release()
	if processRecovery == nil || cap(processRecovery.slots) != recoveryConcurrency || NewDialer(DialConfig{}).recovery != processRecovery {
		t.Fatal("the Dialers do not share the process gate")
	}
}

// tlsEntry serves a over TLS 1.3 and records the server side of each
// carrier's TLS once its handshake is done.
func tlsEntry(t *testing.T, a *Acceptor, ctx context.Context) (addr string, states chan tls.ConnectionState) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cert := testCert(t)
	states = make(chan tls.ConnectionState, 8)
	// One config: its ticket key is what a resumption needs.
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				tc := tls.Server(c, cfg)
				if err := tc.HandshakeContext(ctx); err != nil {
					_ = c.Close()
					return
				}
				states <- tc.ConnectionState()
				a.Serve(ctx, tc, "tls")
			})
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	return ln.Addr().String(), states
}

// A second carrier resumes the TLS of the first, and seal still
// authenticates the client in full: a wrong key on a resumed TLS is refused.
func TestCarrierTLSResumes(t *testing.T) {
	a, err := NewAcceptor(memStore{rec(1, "key-1", true)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	online := make(chan coreapi.Session, 4)
	failed := make(chan coreapi.AuthFailure, 4)
	if err := a.Start(ctx, coreapi.Hooks{
		Online: func(s coreapi.Session) { online <- s },
		AuthFailed: func(f coreapi.AuthFailure) {
			select {
			case failed <- f:
			default:
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	addr, states := tlsEntry(t, a, ctx)
	t.Cleanup(func() {
		cancel()
		_ = a.Close()
	})
	cands, err := connprofile.Parse(addr, "tls")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDialer(DialConfig{Candidates: cands, VKey: "key-1", Agent: "t", Timeout: 3 * time.Second})
	dial := func(d *Dialer) (*clientSession, error) {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()
		s, err := d.Dial(dctx)
		if err != nil {
			return nil, err
		}
		return s.(*clientSession), nil
	}
	first, err := dial(d)
	if err != nil {
		t.Fatal(err)
	}
	if st := <-states; st.DidResume {
		t.Fatal("the first carrier resumed")
	}
	<-online
	// The ticket arrives after the handshake, with the session's first
	// reads; it is there once the first CONFIG is.
	waitFor(t, "first CONFIG", func() bool { return first.appliedSeq() >= 1 })
	_ = first.Close()
	<-first.Done()

	second, err := dial(d)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close(); <-second.Done() }()
	if st := <-states; !st.DidResume {
		t.Fatal("the second carrier did not resume TLS")
	}
	select {
	case s := <-online:
		if s.ClientID() != 1 || s.Record() != zmux.RecordBound.String() {
			t.Fatalf("resumed session: client %d record %s", s.ClientID(), s.Record())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resumed session did not come online")
	}

	// The TLS cache vouches for nothing: seal refuses a wrong key on it.
	bad := NewDialer(DialConfig{Candidates: cands, VKey: "key-2", Agent: "t", Timeout: 2 * time.Second})
	bad.tlsCache = d.tlsCache
	if s, err := dial(bad); err == nil {
		_ = s.Close()
		t.Fatal("a wrong key connected over a resumed TLS")
	}
	if st := <-states; !st.DidResume {
		t.Fatal("the wrong key's carrier did not resume TLS")
	}
	select {
	case f := <-failed:
		if f.Reason != "bad_key" {
			t.Fatalf("refusal %+v, want bad_key", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("seal reported no refusal")
	}
}

// sessionGoroutines is the goroutines that n idle sessions keep, client and
// server side together, per session.
func sessionGoroutines(t *testing.T, n int) float64 {
	t.Helper()
	// More sessions go offline at the end than the env buffers.
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	e := start(t, "tcp", rec(1, "key-1", true))
	go func() {
		for {
			select {
			case <-e.offline:
			case <-stop:
				return
			}
		}
	}()
	settle := func() int {
		var g int
		for range 20 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
			g = runtime.NumGoroutine()
		}
		return g
	}
	before := settle()
	for range n {
		d := e.dialer("key-1")
		d.cfg.Key = newKey(t)
		cs := e.dial(d)
		e.waitOnline()
		waitFor(t, "CONFIG", func() bool { return cs.appliedSeq() >= 1 })
	}
	return float64(settle()-before) / float64(n)
}

// The session loops are zmux callbacks: an idle session keeps no goroutine
// per stream kind, event queue or push loop. Before, a server session kept
// handle, watch, rejectUniStreams, serveStreams and pushLoop (5), and a
// client session serveStreams, watch, the uni accept loop and its waiter
// (4): 9 above zmux's own per pair. Now handle and the waiter remain (2).
func TestSessionResidentGoroutines(t *testing.T) {
	const n = 20
	per := sessionGoroutines(t, n)
	t.Logf("%.2f goroutines per idle session pair (client and server, zmux included)", per)
	base := zmuxPairGoroutines(t)
	t.Logf("%.2f of them are zmux's own", base)
	if own := per - base; own > 2.5 {
		t.Fatalf("%.2f goroutines of this package per session pair, want 2", own)
	}
}

// zmuxPairGoroutines is what a bare zmux session pair over TCP keeps idle.
func zmuxPairGoroutines(t *testing.T) float64 {
	t.Helper()
	const n = 20
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	settle := func() int {
		var g int
		for range 20 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
			g = runtime.NumGoroutine()
		}
		return g
	}
	before := settle()
	var conns []*zmux.Conn
	for range n {
		acc := make(chan *zmux.Conn, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				acc <- nil
				return
			}
			zc, err := zmux.Start(context.Background(), zmux.NetPipe(c), zmux.RoleServer, boundBuffers(&zmux.Config{EnableDatagrams: true}))
			if err != nil {
				acc <- nil
				return
			}
			acc <- zc
		}()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		zc, err := zmux.Start(context.Background(), zmux.NetPipe(c), zmux.RoleClient, boundBuffers(&zmux.Config{EnableDatagrams: true}))
		if err != nil {
			t.Fatal(err)
		}
		sc := <-acc
		if sc == nil {
			t.Fatal("server side did not start")
		}
		conns = append(conns, zc, sc)
	}
	after := settle()
	for _, c := range conns {
		_ = c.Close()
	}
	return float64(after-before) / float64(n)
}
