package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/zmuxio/zmux-go"
)

// cutProxy is a carrier that can be cut: a TCP forwarder that closes its
// connections on demand and, while blocked, accepts no new ones.
type cutProxy struct {
	t       *testing.T
	target  string
	ln      net.Listener
	mu      sync.Mutex
	conns   []net.Conn
	blocked bool
	wg      sync.WaitGroup
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{t: t, target: target, ln: ln}
	p.wg.Go(p.acceptLoop)
	t.Cleanup(func() {
		_ = ln.Close()
		p.cut()
		p.wg.Wait()
	})
	return p
}

func (p *cutProxy) addr() string { return p.ln.Addr().String() }

func (p *cutProxy) acceptLoop() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		blocked := p.blocked
		p.mu.Unlock()
		if blocked {
			_ = c.Close()
			continue
		}
		up, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, up)
		p.mu.Unlock()
		for _, pair := range [][2]net.Conn{{c, up}, {up, c}} {
			p.wg.Go(func() {
				_, _ = io.Copy(pair[0], pair[1])
				_ = pair[0].Close()
				_ = pair[1].Close()
			})
		}
	}
}

// cut closes every carrier.
func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *cutProxy) block(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = v
}

// down blocks the proxy and cuts its carriers: the network is gone.
func (p *cutProxy) down() {
	p.block(true)
	p.cut()
}

func (e *env) resumeDialer(server string, resume time.Duration, pipes string) *Dialer {
	e.t.Helper()
	c, err := connprofile.Parse(server, "tcp")
	if err != nil {
		e.t.Fatal(err)
	}
	return NewDialer(DialConfig{Candidates: c, VKey: "key", Agent: "t", Timeout: 2 * time.Second, Pipes: pipes,
		ResumeTimeout: resume, Exit: plainExit{}, Key: newKey(e.t)})
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			})
		}
	})
	return ln.Addr().String()
}

func (e *env) noOffline() {
	e.t.Helper()
	select {
	case r := <-e.offline:
		e.t.Fatalf("offline during suspension: %s", r)
	default:
	}
}

func (e *env) expectOffline(want string, within time.Duration) {
	e.t.Helper()
	select {
	case r := <-e.offline:
		if r != want {
			e.t.Fatalf("offline reason %q, want %q", r, want)
		}
	case <-time.After(within):
		e.t.Fatalf("no offline within %v", within)
	}
}

func suspendedSoon(t *testing.T, s *serverSession) {
	t.Helper()
	waitUntil(t, "suspension", s.Suspended)
}

// Bytes in flight and written while suspended all arrive, in order, on the
// same session after the only carrier was replaced.
func TestResumeKeepsStreamBytes(t *testing.T) {
	e := startOpts(t, "tcp", Options{ResumeTimeout: 10 * time.Second}, rec(1, "key", true))
	p := newCutProxy(t, e.addr)
	cs := e.dial(e.resumeDialer(p.addr(), 10*time.Second, ""))
	s := e.waitOnline()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := s.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: echoServer(t), WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))

	before := bytes.Repeat([]byte("before-cut."), 2000)
	during := bytes.Repeat([]byte("during-cut."), 2000)
	if _, err := c.Write(before); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(before))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, before) {
		t.Fatalf("before cut: %v", err)
	}

	p.down()
	suspendedSoon(t, s)
	if _, err := c.Write(during); err != nil {
		t.Fatal(err)
	}
	e.noOffline()
	p.block(false)

	got = make([]byte, len(during))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, during) {
		t.Fatalf("after resume: %v", err)
	}
	waitUntil(t, "resume", func() bool { return !s.Suspended() })
	e.noOffline()
	select {
	case <-cs.Done():
		t.Fatalf("npc session ended: %v", cs.Err())
	case o := <-e.online:
		t.Fatalf("a fresh session instead of a resume: %v", o)
	default:
	}
}

// The grace passes: the session ends as closed on both sides, and npc can
// establish again.
func TestResumeTimeoutEndsSession(t *testing.T) {
	e := startOpts(t, "tcp", Options{ResumeTimeout: 400 * time.Millisecond}, rec(1, "key", true))
	p := newCutProxy(t, e.addr)
	d := e.resumeDialer(p.addr(), 400*time.Millisecond, "")
	cs := e.dial(d)
	s := e.waitOnline()
	p.down()
	suspendedSoon(t, s)
	e.expectOffline("closed", 10*time.Second)
	select {
	case <-cs.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("npc session did not end")
	}
	if err := cs.Err(); !errors.Is(err, zmux.ErrResumeTimeout) && !errors.Is(err, zmux.ErrPipesLost) {
		t.Fatalf("session ended by %v", err)
	}
	p.block(false)
	e.dial(d)
	e.waitOnline()
}

// Every pipe of a multi-pipe session lost: the pools rejoin the same session.
func TestResumeMultiPipeAllLost(t *testing.T) {
	e := startOpts(t, "tcp", Options{ResumeTimeout: 10 * time.Second}, rec(1, "key", true))
	p := newCutProxy(t, e.addr)
	cs := e.dial(e.resumeDialer("tcp://"+p.addr()+"?count=2", 10*time.Second, connprofile.PipesMulti))
	s := e.waitOnline()
	waitUntil(t, "two pipes", func() bool { return sumPipes(s) == 2 })
	p.down()
	suspendedSoon(t, s)
	e.noOffline()
	p.block(false)
	waitUntil(t, "resume", func() bool { return !s.Suspended() && sumPipes(s) == 2 })
	e.noOffline()
	select {
	case <-cs.Done():
		t.Fatalf("npc session ended: %v", cs.Err())
	default:
	}
}

// A client disabled, or whose key changed, ends at once and not at the end of
// the grace; the old key is then refused.
func TestSuspendedEndsOnClientChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  coreapi.ClientRecord
	}{
		{"disabled", coreapi.ClientRecord{ID: 1, VKey: "key", Crypt: true}},
		{"key changed", coreapi.ClientRecord{ID: 1, VKey: "other-key", Enabled: true, Crypt: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := startOpts(t, "tcp", Options{ResumeTimeout: 30 * time.Second}, rec(1, "key", true))
			p := newCutProxy(t, e.addr)
			d := e.resumeDialer(p.addr(), 200*time.Millisecond, "")
			cs := e.dial(d)
			s := e.waitOnline()
			p.down()
			suspendedSoon(t, s)
			e.a.ClientChanged(tc.rec)
			e.expectOffline("deleted", 3*time.Second)
			select {
			case <-cs.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("npc session did not end")
			}
			p.block(false)
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			if s2, err := d.Dial(ctx); err == nil {
				_ = s2.Close()
				<-s2.Done()
				t.Fatal("refused key established a session")
			}
		})
	}
}

// A session of the same instance taking over a suspended one, a kick and a
// close end it without waiting for its grace.
func TestSuspendedEndsOnTakeoverAndKick(t *testing.T) {
	t.Run("takeover", func(t *testing.T) {
		e := startOpts(t, "tcp", Options{ResumeTimeout: 30 * time.Second}, rec(1, "key", true))
		p := newCutProxy(t, e.addr)
		key := newKey(t)
		dial := func(addr string) *Dialer {
			c, err := connprofile.Parse(addr, "tcp")
			if err != nil {
				t.Fatal(err)
			}
			return NewDialer(DialConfig{Candidates: c, VKey: "key", Agent: "t", Timeout: 2 * time.Second, Key: key})
		}
		e.dial(dial(p.addr()))
		s := e.waitOnline()
		p.down()
		suspendedSoon(t, s)
		start := time.Now()
		e.dial(dial(e.addr))
		e.waitOnline()
		e.expectOffline("replaced", 3*time.Second)
		if d := time.Since(start); d >= drainTimeout {
			t.Fatalf("takeover took %v", d)
		}
	})
	t.Run("kick", func(t *testing.T) {
		e := startOpts(t, "tcp", Options{ResumeTimeout: 30 * time.Second}, rec(1, "key", true))
		p := newCutProxy(t, e.addr)
		e.dial(e.resumeDialer(p.addr(), 30*time.Second, ""))
		s := e.waitOnline()
		p.down()
		suspendedSoon(t, s)
		start := time.Now()
		e.a.Kick(1, false)
		e.expectOffline("kicked", 3*time.Second)
		if d := time.Since(start); d >= drainTimeout {
			t.Fatalf("kick took %v", d)
		}
	})
}

// A negative timeout on nps is no resume: the session ends with its last
// pipe.
func TestNoResumeWhenDisabled(t *testing.T) {
	e := startOpts(t, "tcp", Options{ResumeTimeout: -1}, rec(1, "key", true))
	p := newCutProxy(t, e.addr)
	cs := e.dial(e.resumeDialer(p.addr(), 30*time.Second, ""))
	s := e.waitOnline()
	p.down()
	e.expectOffline("closed", 3*time.Second)
	if s.Suspended() {
		t.Fatal("suspended without resume")
	}
	select {
	case <-cs.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("npc session did not end")
	}
}

// A redial goes to the next candidate when the last good one stays down, and
// that one becomes the last good one.
func TestRedialFailsOverCandidates(t *testing.T) {
	e := startOpts(t, "tcp", Options{ResumeTimeout: 10 * time.Second}, rec(1, "key", true))
	first, second := newCutProxy(t, e.addr), newCutProxy(t, e.addr)
	d := e.resumeDialer("tcp://"+first.addr()+"|tcp://"+second.addr(), 10*time.Second, "")
	e.dial(d)
	s := e.waitOnline()
	first.down()
	suspendedSoon(t, s)
	waitUntil(t, "resume", func() bool { return !s.Suspended() })
	d.mu.Lock()
	last := d.last
	d.mu.Unlock()
	if want := d.cfg.Candidates[1].Key(); last != want {
		t.Fatalf("last good %q, want %q", last, want)
	}
}

// The grace of npc: CONFIG, else the local setting, else 30 s; and a redial
// source only with resume.
func TestResumeTimeoutSources(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	d := e.resumeDialer(e.addr, 0, "")
	if got := d.resumeTimeout(); got != 30*time.Second {
		t.Fatalf("default %v", got)
	}
	d = e.resumeDialer(e.addr, 7*time.Second, "")
	if got := d.resumeTimeout(); got != 7*time.Second {
		t.Fatalf("local %v", got)
	}
	s := &clientSession{d: d}
	s.apply(1, wire.Snapshot{Session: wire.SessionOptions{ResumeMs: 1500}})
	cfg := d.sessionConfig(s)
	if cfg.ResumeTimeout != 1500*time.Millisecond || cfg.Redial == nil {
		t.Fatalf("from CONFIG: %v, redial %v", cfg.ResumeTimeout, cfg.Redial != nil)
	}
	d = e.resumeDialer(e.addr, -1, "")
	if cfg := d.sessionConfig(&clientSession{d: d}); cfg.ResumeTimeout != 0 || cfg.Redial != nil {
		t.Fatalf("disabled: %v, redial %v", cfg.ResumeTimeout, cfg.Redial != nil)
	}
	d = e.resumeDialer(e.addr, 7*time.Second, connprofile.PipesMulti)
	if cfg := d.sessionConfig(&clientSession{d: d}); cfg.Redial != nil {
		t.Fatal("multi-pipe sessions have their pools, not Redial")
	}
}

// §3.2 for a redial: a Bound try that may fall back is followed by Sealed.
func TestRedialModeFallsBack(t *testing.T) {
	d := NewDialer(DialConfig{VKey: "key"})
	s := &clientSession{d: d}
	c := connprofile.Candidate{Addr: "a:1", Carrier: "tls"}
	ca := carrier{secure: true, network: "n"}
	for i, want := range []zmux.RecordMode{zmux.Bound, zmux.Sealed, zmux.Bound} {
		s.planRedial(c, ca)
		if got := s.redialRecord(zmux.CarrierInfo{}); got != want {
			t.Fatalf("try %d: mode %v, want %v", i, got, want)
		}
	}
	s.redialDone()
	s.planRedial(c, ca)
	if got := s.redialRecord(zmux.CarrierInfo{}); got != zmux.Bound {
		t.Fatalf("after a resume: %v", got)
	}
}
