package bridge

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/model"
)

// resumeProxy is a carrier that can be cut and kept down.
type resumeProxy struct {
	ln      net.Listener
	target  string
	mu      sync.Mutex
	conns   []net.Conn
	blocked bool
	wg      sync.WaitGroup
}

func newResumeProxy(tb testing.TB, target string) *resumeProxy {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	p := &resumeProxy{ln: ln, target: target}
	p.wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			blocked := p.blocked
			p.mu.Unlock()
			var up net.Conn
			if !blocked {
				up, err = net.Dial("tcp", p.target)
			}
			if blocked || err != nil {
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
	})
	tb.Cleanup(func() {
		_ = ln.Close()
		p.down()
		p.wg.Wait()
	})
	return p
}

func (p *resumeProxy) down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = true
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *resumeProxy) up() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = false
}

// countingExit counts the links an npc served.
type countingExit struct {
	coreapi.Exit
	n atomic.Int64
}

func (e *countingExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	e.n.Add(1)
	return e.Exit.Dial(ctx, network, target)
}

func newCountingExit() *countingExit { return &countingExit{Exit: client.NewCoreExit("", 0)} }

// joinVia connects one npc, an instance of its own, through addr.
func (f *coreFixture) joinVia(tb testing.TB, addr string, exit coreapi.Exit, resume time.Duration) coreapi.ClientSession {
	tb.Helper()
	cands, err := connprofile.Parse(addr, "tcp")
	if err != nil {
		tb.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "npc/9.8.7 test/x",
		Key: key, Exit: exit, Timeout: 2 * time.Second, ResumeTimeout: resume}).Dial(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close(); <-s.Done() })
	return s
}

func (f *coreFixture) loadedClient() (*Client, bool) {
	v, ok := f.b.Client.Load(f.client.Id)
	if !ok {
		return nil, false
	}
	return v.(*Client), true
}

func resumeEventually(tb testing.TB, what string, ok func() bool) {
	tb.Helper()
	dl := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(dl) {
			tb.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitSuspended waits until the only node of the client is suspended.
func (f *coreFixture) waitSuspended(tb testing.TB, nodes int) {
	tb.Helper()
	resumeEventually(tb, "suspension", func() bool {
		c, ok := f.loadedClient()
		if !ok {
			return false
		}
		n := 0
		for _, node := range c.nodeList() {
			if node.suspended() {
				n++
			}
		}
		return n == nodes
	})
}

func (f *coreFixture) waitNodes(tb testing.TB, n int) {
	tb.Helper()
	resumeEventually(tb, "nodes", func() bool {
		c, ok := f.loadedClient()
		return ok && c.NodeCount() == n
	})
}

func tcpLink(target string, timeout time.Duration) *conn.Link {
	l := conn.NewLink(conn.LinkTCP, target, false, false, "1.2.3.4:5", false)
	l.Option.NeedAck, l.Option.Timeout = true, timeout
	return l
}

func resumeEcho(tb testing.TB) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	var wg sync.WaitGroup
	tb.Cleanup(func() {
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

func resumeOpts(d time.Duration) session.Options { return session.Options{ResumeTimeout: d} }

// A suspended session is online: health checks do not count it, and only the
// end of its grace makes the client offline, with reason closed.
func TestSuspendedIsNotOffline(t *testing.T) {
	f := newCoreFixtureOpts(t, Primary, model.Config{}, resumeOpts(1500*time.Millisecond))
	p := newResumeProxy(t, f.addr)
	f.joinVia(t, p.ln.Addr().String(), newCountingExit(), 1500*time.Millisecond)
	f.waitNodes(t, 1)
	p.down()
	f.waitSuspended(t, 1)
	for range 5 {
		f.b.pingClients()
		if _, ok := f.loadedClient(); !ok {
			t.Fatal("client went offline during the suspension")
		}
	}
	resumeEventually(t, "end of the grace", func() bool {
		c, ok := f.loadedClient()
		return ok && c.CheckNode() == nil
	})
	f.b.pingClients()
	f.b.pingClients()
	f.b.pingClients()
	if _, ok := f.loadedClient(); ok {
		t.Fatal("client still online after the grace and the health checks")
	}
}

// A request during the suspension waits and goes through as soon as the
// session resumes.
func TestRequestWaitsForResume(t *testing.T) {
	f := newCoreFixtureOpts(t, Primary, model.Config{}, resumeOpts(20*time.Second))
	p := newResumeProxy(t, f.addr)
	f.joinVia(t, p.ln.Addr().String(), newCountingExit(), 20*time.Second)
	f.waitNodes(t, 1)
	target := resumeEcho(t)
	p.down()
	f.waitSuspended(t, 1)

	type result struct {
		c   net.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := f.b.Session(f.client.Id).OpenLink(tcpLink(target, 15*time.Second), 0)
		done <- result{c, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("returned during the suspension: %v", r.err)
	case <-time.After(300 * time.Millisecond):
	}
	p.up()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		_ = r.c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := r.c.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4)
		if _, err := io.ReadFull(r.c, b); err != nil || string(b) != "ping" {
			t.Fatalf("echo %q: %v", b, err)
		}
		_ = r.c.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("did not wake on resume")
	}
}

// With no session to go to, the wait ends at the entry's bound, and it leaves
// nothing running.
func TestRequestFailsAtBound(t *testing.T) {
	f := newCoreFixtureOpts(t, Primary, model.Config{}, resumeOpts(30*time.Second))
	p := newResumeProxy(t, f.addr)
	f.joinVia(t, p.ln.Addr().String(), newCountingExit(), 30*time.Second)
	f.waitNodes(t, 1)
	p.down()
	f.waitSuspended(t, 1)

	base := runtime.NumGoroutine()
	var wg sync.WaitGroup
	const bound = 300 * time.Millisecond
	start := time.Now()
	for range 32 {
		wg.Go(func() {
			_, err := f.b.Session(f.client.Id).OpenLink(tcpLink("127.0.0.1:1", bound), 0)
			var le *coreapi.LinkError
			if !errors.As(err, &le) || le.Reason != "client_offline" {
				t.Errorf("err = %v", err)
			}
		})
	}
	wg.Wait()
	if d := time.Since(start); d < bound || d > 3*time.Second {
		t.Fatalf("wait of %v for a bound of %v", d, bound)
	}
	resumeEventually(t, "goroutines to go", func() bool { return runtime.NumGoroutine() <= base+2 })
}

// While one session is suspended, requests go to another one of the client.
func TestRequestMovesToAnotherSession(t *testing.T) {
	f := newCoreFixtureOpts(t, RoundRobin, model.Config{}, resumeOpts(30*time.Second))
	p := newResumeProxy(t, f.addr)
	cut, other := newCountingExit(), newCountingExit()
	f.joinVia(t, p.ln.Addr().String(), cut, 30*time.Second)
	f.joinVia(t, f.addr, other, 30*time.Second)
	f.waitNodes(t, 2)
	target := resumeEcho(t)
	p.down()
	f.waitSuspended(t, 1)
	cut.n.Store(0)
	for range 6 {
		start := time.Now()
		c, err := f.b.Session(f.client.Id).OpenLink(tcpLink(target, 5*time.Second), 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("waited %v with a usable session", d)
		}
	}
	if other.n.Load() < 6 || cut.n.Load() != 0 {
		t.Fatalf("usable session served %d, suspended %d", other.n.Load(), cut.n.Load())
	}
}

// A client disabled during the suspension ends at once, its waiters with it.
func TestDisableDuringSuspension(t *testing.T) {
	f := newCoreFixtureOpts(t, Primary, model.Config{}, resumeOpts(30*time.Second))
	p := newResumeProxy(t, f.addr)
	f.joinVia(t, p.ln.Addr().String(), newCountingExit(), 30*time.Second)
	f.waitNodes(t, 1)
	p.down()
	f.waitSuspended(t, 1)

	done := make(chan error, 1)
	go func() {
		_, err := f.b.Session(f.client.Id).OpenLink(tcpLink("127.0.0.1:1", 20*time.Second), 0)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	f.client.Status = false
	f.a.ClientChanged(coreapi.ClientRecord{ID: f.client.Id, VKey: f.client.VerifyKey})
	f.b.DelClient(f.client.Id)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter not released")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	if _, ok := f.loadedClient(); ok {
		t.Fatal("client still online")
	}
}
