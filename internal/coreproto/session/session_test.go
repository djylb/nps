package session

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
)

type memStore []coreapi.ClientRecord

func (m memStore) RangeClients(fn func(coreapi.ClientRecord) bool) {
	for _, r := range m {
		if !fn(r) {
			return
		}
	}
}

func rec(id int, vkey string, crypt bool) coreapi.ClientRecord {
	return coreapi.ClientRecord{ID: id, VKey: vkey, Enabled: true, Crypt: crypt}
}

type env struct {
	t        *testing.T
	a        *Acceptor
	addr     string
	carrier  string
	online   chan *serverSession
	offline  chan string
	failed   chan coreapi.AuthFailure
	legacy   chan []byte
	fallback chan []byte
	ctx      context.Context
}

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

func read5(c net.Conn) []byte {
	b := make([]byte, 5)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _ := io.ReadFull(c, b)
	_ = c.Close()
	return b[:n]
}

func start(t *testing.T, carrier string, records ...coreapi.ClientRecord) *env {
	t.Helper()
	a, err := NewAcceptor(memStore(records))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, a: a, carrier: carrier, online: make(chan *serverSession, 8), offline: make(chan string, 8),
		failed: make(chan coreapi.AuthFailure, 8), legacy: make(chan []byte, 2), fallback: make(chan []byte, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	e.ctx = ctx
	var hookWG sync.WaitGroup
	if err := a.Start(ctx, coreapi.Hooks{
		Online:  func(s coreapi.Session) { e.online <- s.(*serverSession) },
		Offline: func(_ coreapi.Session, r string) { e.offline <- r },
		AuthFailed: func(f coreapi.AuthFailure) {
			select {
			case e.failed <- f:
			default:
			}
		},
		Legacy: func(c net.Conn, _ string) {
			hookWG.Go(func() { e.legacy <- read5(c) })
		},
		Fallback: func(c net.Conn, _ *tls.ConnectionState) bool {
			hookWG.Go(func() { e.fallback <- read5(c) })
			return true
		},
	}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.addr = ln.Addr().String()
	cert := testCert(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				if carrier == "tls" {
					c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				}
				a.Serve(ctx, c, carrier)
			})
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		cancel()
		_ = a.Close()
		wg.Wait()
		hookWG.Wait()
	})
	return e
}

func (e *env) cands(addr string) []connprofile.Candidate {
	e.t.Helper()
	c, err := connprofile.Parse(addr, e.carrier)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) dialer(vkey string) *Dialer {
	return NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: vkey, Agent: "t", Timeout: 3 * time.Second})
}

func newKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (e *env) dial(d *Dialer) *clientSession {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, err := d.Dial(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	cs := s.(*clientSession)
	e.t.Cleanup(func() {
		_ = cs.Close()
		<-cs.Done()
	})
	return cs
}

func (e *env) waitOnline() *serverSession {
	e.t.Helper()
	select {
	case s := <-e.online:
		return s
	case <-time.After(5 * time.Second):
		e.t.Fatal("no session came online")
		return nil
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	dead := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(dead) {
			t.Fatal("timed out: " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *clientSession) appliedSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied
}

func TestEstablishTCPSealed(t *testing.T) {
	e := start(t, "tcp", rec(1, "abc", true)) // a 0.34-style short vkey
	cs := e.dial(e.dialer("abc"))
	s := e.waitOnline()
	if s.ClientID() != 1 || s.mode != zmux.Sealed || cs.mode != zmux.Sealed || s.Carrier() != "tcp" || s.Agent() != "t" {
		t.Fatalf("%+v %v", s.t, s.mode)
	}
	waitFor(t, "CONFIG seq 1", func() bool { return cs.appliedSeq() == 1 })
	if cs.d.record != wire.RecordDefault {
		t.Fatalf("record %d after an empty snapshot", cs.d.record)
	}
}

func TestEstablishTLSBound(t *testing.T) {
	e := start(t, "tls", rec(1, "key-1", true))
	cs := e.dial(e.dialer("key-1"))
	if s := e.waitOnline(); s.mode != zmux.Bound || cs.mode != zmux.Bound || s.Carrier() != "tls" {
		t.Fatalf("server %v client %v", s.mode, cs.mode)
	}
}

// tlsProxy terminates TLS and dials the server with new TLS, as a CDN does.
func tlsProxy(t *testing.T, target string) (addr string, carriers *atomic.Int32) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(t)}})
	if err != nil {
		t.Fatal(err)
	}
	carriers = new(atomic.Int32)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			carriers.Add(1)
			wg.Go(func() {
				defer c.Close()
				up, err := tls.Dial("tcp", target, &tls.Config{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer up.Close()
				var both sync.WaitGroup
				both.Go(func() { _, _ = io.Copy(up, c); _ = up.Close() })
				both.Go(func() { _, _ = io.Copy(c, up); _ = c.Close() })
				both.Wait()
			})
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return ln.Addr().String(), carriers
}

func TestBoundFailureFallsToSealedOnNewCarrier(t *testing.T) {
	e := start(t, "tls", rec(1, "key-1", true))
	addr, carriers := tlsProxy(t, e.addr)
	d := NewDialer(DialConfig{Candidates: e.cands(addr), VKey: "key-1", Agent: "t", Timeout: 8 * time.Second})
	cs := e.dial(d)
	if s := e.waitOnline(); s.mode != zmux.Sealed || cs.mode != zmux.Sealed {
		t.Fatalf("server %v client %v", s.mode, cs.mode)
	}
	if n := carriers.Load(); n != 2 {
		t.Fatalf("%d carriers, want 2 (Bound, then a new one for Sealed)", n)
	}
	select {
	case f := <-e.failed:
		t.Fatalf("failing Bound reported as an authentication failure: %+v", f)
	default:
	}
	// The result is remembered: the next session goes straight to Sealed.
	_ = cs.Close()
	<-cs.Done()
	e.dial(d)
	if s := e.waitOnline(); s.mode != zmux.Sealed {
		t.Fatal(s.mode)
	}
	if n := carriers.Load(); n != 3 {
		t.Fatalf("%d carriers, want 3", n)
	}
}

func TestCryptOffDeliversTrusted(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", false))
	d := e.dialer("key-1")
	cs := e.dial(d)
	if s := e.waitOnline(); s.mode != zmux.Sealed {
		t.Fatalf("first pipe in %v, want the on rules", s.mode)
	}
	waitFor(t, "record 4", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.record == wire.RecordTrusted
	})
	_ = cs.Close()
	<-cs.Done()
	again := e.dial(d)
	if s := e.waitOnline(); s.mode != zmux.Trusted || again.mode != zmux.Trusted {
		t.Fatalf("second session in %v", s.mode)
	}
	waitFor(t, "CONFIG", func() bool { return again.appliedSeq() == 1 })
}

func TestTrustedLostBeforeConfigForgetsRecord(t *testing.T) {
	d := NewDialer(DialConfig{VKey: "k"})
	d.setRecord(wire.RecordTrusted)
	s := &clientSession{d: d, mode: zmux.Trusted}
	s.ended()
	if d.record != wire.RecordDefault {
		t.Fatal("record kept")
	}
	d.setRecord(wire.RecordTrusted)
	s.apply(1, wire.Snapshot{Record: wire.RecordTrusted})
	s.ended()
	if d.record != wire.RecordTrusted {
		t.Fatal("record dropped after a CONFIG")
	}
}

func TestTrustedRefusedForCryptOnClient(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	d := e.dialer("key-1")
	d.setRecord(wire.RecordTrusted)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if s, err := d.Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("Trusted accepted")
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("took %v: a refused Trusted must close at once", time.Since(start))
	}
	select {
	case f := <-e.failed:
		if f.Client != 1 || f.Reason != "forbidden" || f.Flag != "trusted" {
			t.Fatalf("%+v", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not reported")
	}
}

func TestAllowJoinMode(t *testing.T) {
	a := &Acceptor{table: newHandleTable(memStore{rec(1, "a", true), rec(2, "b", false)})}
	peer := func(id int) *seal.Peer { return &seal.Peer{Value: &ticket{client: id}} }
	for _, c := range []struct {
		m    zmux.RecordMode
		id   int
		want bool
	}{{zmux.Sealed, 1, true}, {zmux.Bound, 1, true}, {zmux.Trusted, 1, false}, {zmux.Trusted, 2, true}, {zmux.Unmanaged, 2, false}} {
		if got := a.allowJoinMode(zmux.CarrierInfo{}, c.m, peer(c.id)); got != c.want {
			t.Errorf("mode %v client %d: %v", c.m, c.id, got)
		}
	}
	if onReject(zmux.RejectInfo{Mode: zmux.Bound}) != zmux.RejectClose || onReject(zmux.RejectInfo{Mode: zmux.Trusted}) != zmux.RejectClose ||
		onReject(zmux.RejectInfo{Mode: zmux.Sealed}) != zmux.RejectStall || onReject(zmux.RejectInfo{Profile: 2}) != zmux.RejectStall {
		t.Fatal("OnReject")
	}
}

// countingProxy forwards to target and counts the bytes coming back.
func countingProxy(t *testing.T, target string) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	back := new(atomic.Int64)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer c.Close()
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				var both sync.WaitGroup
				both.Go(func() { _, _ = io.Copy(up, c); _ = up.Close() })
				both.Go(func() {
					n, _ := io.Copy(c, up)
					back.Add(n)
					_ = c.Close()
				})
				both.Wait()
			})
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return ln.Addr().String(), back
}

func TestWrongVKeySendsNothing(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	addr, back := countingProxy(t, e.addr)
	d := NewDialer(DialConfig{Candidates: e.cands(addr), VKey: "other", Timeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if s, err := d.Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("wrong vkey connected")
	}
	if n := back.Load(); n != 0 {
		t.Fatalf("%d bytes came back", n)
	}
}

func TestKnownHandleWrongPSK(t *testing.T) {
	e := start(t, "tcp", rec(7, "key-7", true))
	addr, back := countingProxy(t, e.addr)
	handle, _, err := wire.Derive("key-7")
	if err != nil {
		t.Fatal(err)
	}
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = seal.Establish(ctx, zmux.NetPipe(nc), &seal.ClientConfig{AllowPSKOnly: true, Handle: handle, PSK: make([]byte, 32)})
	if !errors.Is(err, seal.ErrNoReply) {
		t.Fatalf("%v", err)
	}
	select {
	case f := <-e.failed:
		if f.Client != 7 || f.Reason != "bad_key" {
			t.Fatalf("%+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not reported")
	}
	if back.Load() != 0 {
		t.Fatal("bytes came back")
	}
}

func TestAdmissionRouting(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	send := func(b []byte) net.Conn {
		c, err := net.Dial("tcp", e.addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if _, err := c.Write(b); err != nil {
			t.Fatal(err)
		}
		return c
	}
	got := func(ch chan []byte, want string) {
		t.Helper()
		select {
		case b := <-ch:
			if string(b) != want {
				t.Fatalf("replayed %q, want %q", b, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("not routed")
		}
	}
	send([]byte("TST12"))
	got(e.legacy, "TST12")
	send([]byte("GET /"))
	got(e.fallback, "GET /")
	send([]byte("TRACE"))
	got(e.fallback, "TRACE")
	unknown := append([]byte{1, 0}, make([]byte, wire.HandleLen)...)
	send(unknown)
	got(e.fallback, string(unknown[:5]))
	// a Join with a locator nobody has
	join := append([]byte{2, 1}, make([]byte, 300)...)
	send(join)
	got(e.fallback, string(join[:5]))
}

func TestTakeoverAndCoexistence(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	k1, k2 := newKey(t), newKey(t)
	mk := func(k *ecdh.PrivateKey) *Dialer {
		return NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key-1", Key: k, Timeout: 3 * time.Second})
	}
	a := e.dial(mk(k1))
	e.waitOnline()
	b := e.dial(mk(k2)) // another instance: both stay
	e.waitOnline()
	select {
	case <-a.Done():
		t.Fatal("different instance closed the first")
	case <-time.After(200 * time.Millisecond):
	}
	e.dial(mk(k1)) // the same instance takes over
	e.waitOnline()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not taken over")
	}
	var appErr *zmux.ApplicationError
	if !errors.As(a.Err(), &appErr) || appErr.ErrorCode != zmux.ApplicationErrorCode(wire.CodeDrain) {
		t.Fatalf("closed with %v", a.Err())
	}
	if r := <-e.offline; r != "replaced" {
		t.Fatal(r)
	}
	select {
	case <-b.Done():
		t.Fatal("other instance closed")
	default:
	}
}

func TestPin(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	right := seal.NodeKeyID(e.a.key.PublicKey().Bytes())
	wrong := right
	wrong[0] ^= 1
	bad := NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key-1", Pin: wrong[:], Timeout: 2 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if s, err := bad.Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("pin mismatch accepted")
	}
	good := NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "key-1", Pin: right[:], Timeout: 3 * time.Second})
	e.dial(good)
	e.waitOnline()
}

func TestConfigSeq(t *testing.T) {
	s := &clientSession{d: NewDialer(DialConfig{})}
	var got []uint64
	s.onApply = func(seq uint64, _ wire.Snapshot) { got = append(got, seq) }
	for _, seq := range []uint64{2, 2, 1, 5, 3} {
		s.apply(seq, wire.Snapshot{})
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Fatal(got)
	}
}

func TestConfigMalformedKeepsOld(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	cs := e.dial(e.dialer("key-1"))
	s := e.waitOnline()
	waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() == 1 })
	st, err := s.zc.OpenUniStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreamble(wire.AppendConfigOpen(9)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte{0xff}); err != nil { // truncated varint
		t.Fatal(err)
	}
	_ = st.Close()
	s.seq.Store(1)
	if err := s.pushConfig(&wire.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "seq 2", func() bool { return cs.appliedSeq() == 2 })
}

func TestClientChangesTakeEffect(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	// added after the table was built
	e.a.ClientChanged(rec(2, "key-2", true))
	cs := e.dial(e.dialer("key-2"))
	s := e.waitOnline()
	if s.ClientID() != 2 {
		t.Fatal(s.ClientID())
	}
	// off: pushed, session stays
	e.a.ClientChanged(rec(2, "key-2", false))
	waitFor(t, "record 4 pushed", func() bool { return cs.appliedSeq() == 2 })
	// on: drained
	e.a.ClientChanged(rec(2, "key-2", true))
	select {
	case <-cs.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not drained")
	}
	var appErr *zmux.ApplicationError
	if !errors.As(cs.Err(), &appErr) || appErr.ErrorCode != zmux.ApplicationErrorCode(wire.CodeDrain) {
		t.Fatal(cs.Err())
	}
	<-e.offline
	// removed: closed with NPS_AUTH, and the handle is gone
	again := e.dial(e.dialer("key-2"))
	e.waitOnline()
	e.a.ClientRemoved(2)
	select {
	case <-again.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not closed")
	}
	if !errors.As(again.Err(), &appErr) || appErr.ErrorCode != zmux.ApplicationErrorCode(wire.CodeAuth) {
		t.Fatal(again.Err())
	}
	if e.a.table.lookup(mustHandle(t, "key-2")) != nil {
		t.Fatal("handle kept")
	}
	if r := <-e.offline; r != "deleted" {
		t.Fatal(r)
	}
}

func mustHandle(t *testing.T, vkey string) []byte {
	t.Helper()
	h, _, err := wire.Derive(vkey)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDisabledClientRefused(t *testing.T) {
	r := rec(3, "key-3", true)
	r.Enabled = false
	e := start(t, "tcp", r)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	d := e.dialer("key-3")
	d.cfg.Timeout = time.Second
	if s, err := d.Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("disabled client connected")
	}
	select {
	case f := <-e.failed:
		if f.Client != 3 || f.Reason != "disabled" {
			t.Fatalf("%+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not reported")
	}
}

func TestWebSocketRouting(t *testing.T) {
	e := start(t, "tcp", rec(1, "key-1", true))
	var legacy atomic.Int32
	h, err := e.a.WebSocket(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		legacy.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	// The -type ws way of writing it sends the subprotocol.
	old, err := connprofile.Parse(addr+"/ws", "ws")
	if err != nil {
		t.Fatal(err)
	}
	cs := e.dial(NewDialer(DialConfig{Candidates: old, VKey: "key-1", Timeout: 3 * time.Second}))
	waitFor(t, "CONFIG", func() bool { return cs.appliedSeq() == 1 })
	if s := e.waitOnline(); s.Carrier() != "ws" {
		t.Fatalf("carrier %q", s.Carrier())
	}
	if legacy.Load() != 0 {
		t.Fatal("binary went to the old protocol")
	}
	// A URL candidate sends none, and on the bridge path that is the old one.
	url, err := connprofile.Parse("http://"+addr+"/ws", "ws")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if s, err := NewDialer(DialConfig{Candidates: url, VKey: "key-1", Timeout: 2 * time.Second}).Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("no subprotocol reached the new protocol")
	}
	if legacy.Load() != 1 {
		t.Fatalf("legacy saw %d requests", legacy.Load())
	}
}
