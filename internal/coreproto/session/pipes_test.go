package session

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
)

// withMaxPipes is start with a client_max_pipes.
func withMaxPipes(t *testing.T, limit int, records ...coreapi.ClientRecord) *env {
	t.Helper()
	e := start(t, "tcp", records...)
	e.a.maxPipes = limit
	return e
}

func (e *env) multi(vkey, server string) *Dialer {
	e.t.Helper()
	c, err := connprofile.Parse(server, "tcp")
	if err != nil {
		e.t.Fatal(err)
	}
	return NewDialer(DialConfig{Candidates: c, VKey: vkey, Agent: "t", Timeout: 3 * time.Second, Pipes: connprofile.PipesMulti})
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	dl := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(dl) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func sumPipes(s *serverSession) int { return len(s.zc.Pipes()) }

// A candidate that is down makes way for the next.
func TestFailoverAmongCandidates(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	c, err := connprofile.Parse("tcp://"+dead+"|tcp://"+e.addr, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDialer(DialConfig{Candidates: c, VKey: "key", Agent: "t", Timeout: 3 * time.Second})
	e.dial(d)
	e.waitOnline()
}

// Two pipes of a session: the pool joins the second, and a loss of one
// leaves the session up with traffic flowing, the pool replacing the pipe.
func TestTwoPipesSurviveLoss(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	cs := e.dial(e.multi("key", "tcp://"+e.addr+"?count=2"))
	s := e.waitOnline()
	waitUntil(t, "two pipes", func() bool { return sumPipes(s) == 2 && len(cs.zc.Pipes()) == 2 })
	first := cs.zc.Pipes()[0]
	_ = first.Abort(errors.New("cut"))
	st, err := s.zc.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	waitUntil(t, "replacement", func() bool {
		ps := cs.zc.Pipes()
		return len(ps) == 2 && ps[0].ID() != first.ID() && cs.zc.Context().Err() == nil
	})
}

// A standby pipe is built but marked; the tier past best+1 is left out.
func TestStandbyAndTierCutoff(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	addr := e.addr
	far := countListener(t)
	cs := e.dial(e.multi("key", "tcp://"+addr+"?tier=0|tcp://"+addr+"?tier=0&standby=1&weight=2|tcp://"+far.addr()+"?tier=5"))
	e.waitOnline()
	waitUntil(t, "two pipes", func() bool { return len(cs.zc.Pipes()) == 2 })
	var standby int
	for _, st := range cs.zc.PipeStats() {
		if st.Standby {
			standby++
		}
	}
	if standby != 1 {
		t.Fatalf("%d standby pipes, want 1", standby)
	}
	time.Sleep(200 * time.Millisecond)
	if n := far.n.Load(); n != 0 {
		t.Fatalf("tier 5 was dialed %d times", n)
	}
}

// Standby takes over when the active pipe goes.
func TestStandbyTakeover(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	cs := e.dial(e.multi("key", "tcp://"+e.addr+"|tcp://"+e.addr+"?standby=1"))
	s := e.waitOnline()
	waitUntil(t, "two pipes", func() bool { return len(cs.zc.Pipes()) == 2 })
	for _, p := range cs.zc.Pipes() {
		if st, ok := p.Stats(); ok && !st.Standby {
			_ = p.Abort(errors.New("cut"))
		}
	}
	// Traffic goes over what is left at once, before any replacement.
	done := make(chan error, 1)
	go func() {
		st, err := cs.zc.OpenStream()
		if err != nil {
			done <- err
			return
		}
		_, err = st.Write([]byte("x"))
		_ = st.Close()
		done <- err
	}()
	got := make(chan struct{})
	go func() {
		st, err := s.zc.AcceptStream(e.ctx)
		if err == nil {
			_, _ = io.ReadAll(st)
			close(got)
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write stuck")
	}
}

// One-direction candidates become an uplink and a downlink.
func TestDirs(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	cs := e.dial(e.multi("key", "tcp://"+e.addr+"|tcp://"+e.addr+"?dirs=up|tcp://"+e.addr+"?dirs=down"))
	e.waitOnline()
	waitUntil(t, "three pipes", func() bool { return len(cs.zc.Pipes()) == 3 })
	var up, down int
	for _, st := range cs.zc.PipeStats() {
		switch {
		case st.Outbound == zmux.OutboundNone:
			down++
		case st.Inbound == zmux.InboundNone:
			up++
		}
	}
	if up != 1 || down != 1 {
		t.Fatalf("up %d down %d, want one each", up, down)
	}
}

func TestClientMaxPipes(t *testing.T) {
	t.Run("first pipe", func(t *testing.T) {
		e := withMaxPipes(t, 1, rec(1, "key", true))
		e.dial(e.dialer("key"))
		e.waitOnline()
		// Another instance is over the limit; the first stays.
		other := NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key", Agent: "t", Timeout: 1500 * time.Millisecond, Key: newKey(t)})
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if s, err := other.Dial(ctx); err == nil {
			_ = s.Close()
			<-s.Done()
			t.Fatal("second instance admitted over the limit")
		}
		f := <-e.failed
		if f.Reason != "pipes" {
			t.Fatalf("reason %q", f.Reason)
		}
	})
	t.Run("join refused, existing pipe stays", func(t *testing.T) {
		e := withMaxPipes(t, 1, rec(1, "key", true))
		cs := e.dial(e.multi("key", "tcp://"+e.addr+"?count=2"))
		s := e.waitOnline()
		select {
		case f := <-e.failed:
			if f.Reason != "pipes" {
				t.Fatalf("reason %q", f.Reason)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("no join was refused")
		}
		if sumPipes(s) != 1 || cs.zc.Context().Err() != nil {
			t.Fatalf("pipes %d", sumPipes(s))
		}
	})
	t.Run("takeover by the same instance is not counted", func(t *testing.T) {
		e := withMaxPipes(t, 1, rec(1, "key", true))
		key := newKey(t)
		d := NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key", Agent: "t", Timeout: 3 * time.Second, Key: key})
		e.dial(d)
		e.waitOnline()
		e.dial(NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key", Agent: "t", Timeout: 3 * time.Second, Key: key}))
		e.waitOnline()
	})
}

// A Join whose proof does not hold is refused and leaves the session alone.
func TestJoinWrongKey(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	cs := e.dial(e.dialer("key"))
	cs2 := func() *clientSession {
		d := e.multi("key", "tcp://"+e.addr)
		d.cfg.Key = newKey(t)
		return e.dial(d)
	}()
	e.waitOnline()
	e.waitOnline()
	proxy := flipProxy(t, e.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	nc, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs2.zc.AddPipe(ctx, zmux.NetPipe(nc), zmux.PipeOptions{}); err == nil {
		t.Fatal("tampered join accepted")
	}
	waitUntil(t, "the refused pipe gone", func() bool { return len(cs2.zc.Pipes()) == 1 })
	if cs2.zc.Context().Err() != nil || cs.zc.Context().Err() != nil {
		t.Fatal("session harmed by a refused join")
	}
}

// flipProxy forwards to target with the last byte of the first write flipped.
func flipProxy(t *testing.T, target string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = ln.Close()
		close(done)
		wg.Wait()
	})
	wg.Go(func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		u, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer func() { _ = u.Close() }()
		wg.Go(func() { _, _ = io.Copy(c, u); _ = c.Close() })
		buf := make([]byte, 4096)
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		buf[n-1] ^= 0xff
		_, _ = u.Write(buf[:n])
		go func() { <-done; _ = c.Close(); _ = u.Close() }()
		_, _ = io.Copy(u, c)
	})
	return ln.Addr().String()
}

type counted struct {
	ln net.Listener
	n  atomic.Int32
}

func (c *counted) addr() string { return c.ln.Addr().String() }

func countListener(t *testing.T) *counted {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &counted{ln: ln}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			c.n.Add(1)
			_ = nc.Close()
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return c
}

func quicServer(t *testing.T, e *env) string {
	t.Helper()
	l, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(t)}, NextProtos: []string{"h3", "nps"}}, e.a.QUICConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			qc, err := l.Accept(e.ctx)
			if err != nil {
				return
			}
			wg.Go(func() {
				e.a.ServeQUIC(e.ctx, qc)
				_ = qc.CloseWithError(0, "")
			})
		}
	})
	t.Cleanup(func() { _ = l.Close(); wg.Wait() })
	return l.Addr().String()
}

// A session on QUIC is Bound by default, with ALPN h3.
func TestQUICSession(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	addr := quicServer(t, e)
	c, err := connprofile.Parse("quic://"+addr, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	connprofile.UseCoreDefaults(c)
	if c[0].ALPN[0] != "h3" {
		t.Fatalf("alpn %v", c[0].ALPN)
	}
	cs := e.dial(NewDialer(DialConfig{Candidates: c, VKey: "key", Agent: "t", Timeout: 5 * time.Second}))
	s := e.waitOnline()
	if cs.mode != zmux.Bound || s.Carrier() != "quic" {
		t.Fatalf("mode %v carrier %q", cs.mode, s.Carrier())
	}
}

// A QUIC connection whose first stream is no carrier just ends: the old
// protocol is told apart by ALPN before the connection gets here (§12).
func TestQUICNonCarrierStreamEnds(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	addr := quicServer(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	qc, err := quic.DialAddr(ctx, addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = qc.CloseWithError(0, "") }()
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("TST12")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-qc.Context().Done():
	case <-ctx.Done():
		t.Fatal("connection not ended")
	}
}

func TestOversizeUDPAndLinkIDRefused(t *testing.T) {
	if got := wire.MaxUDPPayload; got != 65535 {
		t.Fatal(got)
	}
	s := &clientSession{d: NewDialer(DialConfig{Exit: nil})}
	o := &wire.StreamOpen{Op: wire.OpTCP, LinkID: 7}
	if c := s.checkOpen(o); c != wire.CodeMalformed {
		t.Fatalf("link_id on tcp: %#x", c)
	}
}

func TestLivenessDefaults(t *testing.T) {
	l := livenessOf(wire.SessionOptions{})
	if l.KeepAlivePeriod != 0 || l.ProbeInterval != 0 || l.DeadTimeout != 0 {
		t.Fatalf("%+v", l)
	}
	l = livenessOf(wire.SessionOptions{IdleS: 9, DeadS: 3})
	if l.KeepAlivePeriod != 9*time.Second || l.ProbeInterval != 9*time.Second || l.DeadTimeout != 3*time.Second {
		t.Fatalf("%+v", l)
	}
}

func TestSchedulerFromConfig(t *testing.T) {
	s := wire.Snapshot{SchedulerUp: wire.SchedulerSpreadETA, HasSchedulerUp: true}
	got, err := wire.ParseSnapshot(wire.AppendSnapshot(nil, &s))
	if err != nil || !got.HasSchedulerUp || got.SchedulerUp != wire.SchedulerSpreadETA {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ := wire.ParseSnapshot(nil); got.HasSchedulerUp {
		t.Fatal("absent scheduler_up read as present")
	}
}

func TestPipesOf(t *testing.T) {
	c, err := connprofile.Parse("tcp://a:1?count=2|tcp://a:2?dirs=up,down", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if n := pipesOf(c); n != 4 {
		t.Fatalf("%d", n)
	}
}

// Further pipes join over QUIC connections of their own.
func TestQUICJoin(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	addr := quicServer(t, e)
	cs := e.dial(e.multi("key", "quic://"+addr+"?count=2&alpn=h3"))
	s := e.waitOnline()
	waitUntil(t, "two pipes over quic", func() bool { return len(cs.zc.Pipes()) == 2 && sumPipes(s) == 2 })
}

// UDP crosses a QUIC session as QUIC datagrams and as stream packets.
func TestUDPOverQUIC(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	addr := quicServer(t, e)
	c, err := connprofile.Parse("quic://"+addr, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	connprofile.UseCoreDefaults(c)
	e.dial(NewDialer(DialConfig{Candidates: c, VKey: "key", Agent: "t", Timeout: 5 * time.Second, Exit: plainExit{}}))
	s := e.waitOnline()
	target := listenUDPEcho(t)
	for _, datagrams := range []bool{false, true} {
		lc, err := s.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkUDP, Target: target, WantReply: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("negotiated=%v", lc.(*udpConn).datagrams)
		lc.(*udpConn).datagrams = datagrams
		f := netx.NewFramedConn(lc, netx.WithFramedDatagrams())
		udpExchange(t, f, []byte("ping"), func(b []byte) {
			if string(b) != "ping" {
				t.Fatalf("echo %q", b)
			}
		})
		t.Logf("datagrams=%v ok, sent=%d", datagrams, datagramsSent(lc))
		_ = f.Close()
	}
}

// A launch link with an alt and pipes=multi builds the pipes of its
// candidates: count pipes on the main address and a standby one on the alt.
func TestLaunchLinkBuildsMultiPipes(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	l, err := connprofile.ParseLink("npc://key@" + e.addr + "?type=tcp&pipes=multi&count=2&alt=tcp://" + e.addr + "?standby=1;weight=2")
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := connprofile.ParsePipes(l.Common["pipes"]); err != nil || mode != connprofile.PipesMulti {
		t.Fatalf("%q %v", mode, err)
	}
	cs := e.dial(e.multi(l.VKey, l.Server))
	e.waitOnline()
	waitUntil(t, "three pipes", func() bool { return len(cs.zc.Pipes()) == 3 })
	standby := 0
	for _, st := range cs.zc.PipeStats() {
		if st.Standby {
			standby++
		}
	}
	if standby != 1 {
		t.Fatalf("%d standby pipes, want 1", standby)
	}
}
