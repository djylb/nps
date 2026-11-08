package session

import (
	"bytes"
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
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxquic"
)

// quicPuncher makes direct paths on loopback: the provider listens and sends
// its address over the control stream, the visitor connects to it.
type quicPuncher struct {
	t     *testing.T
	fail  atomic.Bool
	punch atomic.Int64
}

func (p *quicPuncher) Punch(ctx context.Context, ctrl net.Conn, _, accept bool) (*coreapi.DirectPath, error) {
	p.punch.Add(1)
	if p.fail.Load() {
		return nil, errors.New("no path")
	}
	if accept {
		ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(p.t)}, NextProtos: []string{"h3"}}, zmuxquic.QUICConfig(nil))
		if err != nil {
			return nil, err
		}
		if _, err := ctrl.Write([]byte(ln.Addr().String() + "\n")); err != nil {
			_ = ln.Close()
			return nil, err
		}
		qc, err := ln.Accept(ctx)
		if err != nil {
			_ = ln.Close()
			return nil, err
		}
		return &coreapi.DirectPath{QUIC: qc, Close: func() { _ = qc.CloseWithError(0, ""); _ = ln.Close() }}, nil
	}
	var buf [64]byte
	n := 0
	for n == 0 || buf[n-1] != '\n' {
		m, err := ctrl.Read(buf[n:])
		if err != nil {
			return nil, err
		}
		n += m
	}
	qc, err := quic.DialAddr(ctx, string(buf[:n-1]), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}, zmuxquic.QUICConfig(nil))
	if err != nil {
		return nil, err
	}
	return &coreapi.DirectPath{QUIC: qc, Close: func() { _ = qc.CloseWithError(0, "") }}, nil
}

// fixedExit picks the target of a stream that names none.
type fixedExit struct {
	plainExit
	target string
}

func (e fixedExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	if target == "" {
		target = e.target
	}
	return e.plainExit.Dial(ctx, network, target)
}

type peerEnv struct {
	*env
	punch    *quicPuncher
	provider *clientSession
	outer    *OuterSessions
	valid    atomic.Bool
	// exitTarget is the target the last server-exit stream named.
	exitTarget atomic.Pointer[string]
	echo       string
}

// allowAll is the exit of a provider that reaches everything.
func startPeer(t *testing.T, serverExit bool) *peerEnv {
	t.Helper()
	return startPeerOpts(t, serverExit, 0)
}

func startPeerLanes(t *testing.T, lanes int) *peerEnv {
	t.Helper()
	return startPeerOpts(t, false, lanes)
}

func startPeerOpts(t *testing.T, serverExit bool, lanes int) *peerEnv {
	t.Helper()
	p := &peerEnv{punch: &quicPuncher{t: t}, echo: listenEcho(t)}
	p.valid.Store(true)
	p.env = startHooks(t, coreapi.Hooks{
		ResolveLink: func(client int, r coreapi.LinkResolve) (*coreapi.LinkGrant, error) {
			if string(r.Access) != "secret" {
				return nil, linkError(wire.CodeNotAllowed)
			}
			return &coreapi.LinkGrant{Tunnel: 7, Provider: 1, ServerExit: serverExit,
				Policy: coreapi.LinkPolicy{Targets: []string{p.echo}},
				Valid:  p.valid.Load,
				ServeExit: func(ctx context.Context, c net.Conn, _, target string) {
					if a, ok := c.RemoteAddr().(*net.TCPAddr); !ok || !a.IP.IsLoopback() {
						t.Errorf("remote %v", c.RemoteAddr())
					}
					p.exitTarget.Store(&target)
					u, err := net.Dial("tcp", p.echo)
					if err == nil {
						_, _, _ = netx.Relay(ctx, c, u, netx.WithRelayHalfClose())
					}
				}}, nil
		},
		Coordinate: func(ctx context.Context, q coreapi.PunchPair) {
			_, _, _ = netx.Relay(ctx, q.Visitor, q.Provider)
		},
	}, rec(1, "prov", true), rec(2, "vis", true), rec(3, "other", true))
	d := func(vkey string) DialConfig {
		return DialConfig{Candidates: p.cands(p.addr), VKey: vkey, Agent: "t", Timeout: 3 * time.Second, Puncher: p.punch, Exit: plainExit{}, MaxLanes: lanes,
			PeerExit: func(coreapi.LinkPolicy) (coreapi.Exit, error) { return fixedExit{plainExit{}, p.echo}, nil }}
	}
	p.provider = p.dial(NewDialer(d("prov")))
	p.waitOnline()
	p.outer = NewOuterSessions(NewDialer(d("vis")))
	t.Cleanup(p.outer.Close)
	return p
}

func (p *peerEnv) visitor(t *testing.T, mode uint64, idle time.Duration) *Visitor {
	t.Helper()
	v := NewVisitor(VisitorConfig{Outer: p.outer, Access: []byte("secret"), Mode: mode, RelayIdle: idle, Tick: 20 * time.Millisecond, Timeout: 5 * time.Second})
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func peerCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func echoOnce(t *testing.T, ctx context.Context, v *Visitor, msg string) {
	t.Helper()
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()
	checkEcho(t, c, msg)
}

func checkEcho(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := make([]byte, len(msg))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != msg {
		t.Fatalf("echo %q %v", b, err)
	}
}

func waitPipes(t *testing.T, v *Visitor, what string, f func(direct, relay bool) bool) {
	t.Helper()
	waitUntil(t, what, func() bool {
		l := v.live()
		if l == nil || l.zc == nil {
			return false
		}
		var d, r bool
		for _, p := range l.zc.PipeStats() {
			if usable(p) {
				d = d || isDirectPipe(p)
				r = r || !isDirectPipe(p)
			}
		}
		return f(d, r)
	})
}

func TestPeerRelayMode(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnRelay, 10*time.Millisecond)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "relay")
	time.Sleep(200 * time.Millisecond)
	waitPipes(t, v, "relay stays", func(d, r bool) bool { return r && !d })
	if p.punch.punch.Load() != 0 {
		t.Fatal("relay mode punched")
	}
}

func TestPeerDirectMode(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	echoOnce(t, peerCtx(t), v, "direct")
	waitPipes(t, v, "direct only", func(d, r bool) bool { return d && !r })
}

// Auto: relay first, direct joins, relay closes idle and the visitor leaves
// the server; a lost direct path comes back through a new relay, the stream
// surviving.
func TestPeerAutoRelayClosesAndRejoins(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnAuto, 100*time.Millisecond)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	checkEcho(t, c, "first")
	waitPipes(t, v, "direct alone", func(d, r bool) bool { return d && !r })
	waitUntil(t, "outer idle", func() bool { return p.outer.live() == nil })
	checkEcho(t, c, "direct only")

	p.punch.fail.Store(true)
	v.live().closeDirect()
	waitPipes(t, v, "relay back", func(_, r bool) bool { return r })
	checkEcho(t, c, "after rejoin")

	p.punch.fail.Store(false)
	v.live().mu.Lock()
	v.live().nextTry = time.Time{}
	v.live().mu.Unlock()
	waitPipes(t, v, "direct again", func(d, r bool) bool { return d && !r })
	checkEcho(t, c, "direct again")
}

func TestPeerRevokedByServer(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "up")
	l := v.live()
	p.a.RevokeLinks(func(tunnel, visitor, provider int) bool { return tunnel == 7 && visitor == 2 && provider == 1 })
	waitUntil(t, "nested closed", func() bool { return l.ended() })
}

func linkReply(t *testing.T, ctx context.Context, s *clientSession) wire.LinkReply {
	t.Helper()
	info, _ := wire.AppendLinkOpen(&wire.LinkOpen{Access: []byte("secret"), ConnMode: wire.ConnRelay})
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(info)
	_ = st.Flush()
	rep, err := wire.ReadReply(st, replyLimit)
	if err != nil || rep.Code != 0 {
		t.Fatal(rep, err)
	}
	r, err := wire.ParseLinkReply(rep.Fields)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func wantPipeRefused(t *testing.T, ctx context.Context, s *clientSession, id uint64) {
	t.Helper()
	info, _ := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpPipe, LinkID: id})
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(info)
	_ = st.Flush()
	wantReset(t, st, wire.CodeNotAllowed)
}

// A link_id is the visitor client's, and only while the grant holds (§4.6).
func TestPeerPathChecks(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	other := p.dial(NewDialer(DialConfig{Candidates: p.cands(p.addr), VKey: "other", Agent: "t", Timeout: 3 * time.Second}))
	wantPipeRefused(t, ctx, other, r.LinkID)
	wantPipeRefused(t, ctx, vis, r.LinkID+1)
	p.valid.Store(false)
	wantPipeRefused(t, ctx, vis, r.LinkID)
}

// A visitor that reconnects still holds its link.
func TestPeerVisitorOfflineKeepsLink(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	_ = vis.Close()
	<-vis.Done()
	if p.a.linkOf(r.LinkID) == nil {
		t.Fatal("link gone with the visitor")
	}
	again, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l := p.a.linkOf(r.LinkID); l == nil || l.visitor != 2 || again == nil {
		t.Fatal("link lost")
	}
}

// The session of a client that provides a live link stays, whatever its
// visitors need; p2p_visitor_keep_server keeps it too.
func TestOuterMayDisconnect(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	s, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVisitor(VisitorConfig{Outer: p.outer})
	defer v.Close()
	s.mu.Lock()
	s.provided = map[uint64]*providerLink{1: {}}
	s.mu.Unlock()
	p.outer.setNeed(v, false)
	if p.outer.live() == nil {
		t.Fatal("a provider's session closed")
	}
	s.mu.Lock()
	s.provided = nil
	s.mu.Unlock()
	p.outer.Keep = true
	p.outer.setNeed(v, false)
	if p.outer.live() == nil {
		t.Fatal("kept session closed")
	}
	p.outer.Keep = false
	p.outer.Hosting = func() bool { return true }
	p.outer.setNeed(v, false)
	if p.outer.live() == nil {
		t.Fatal("hosting session closed")
	}
	p.outer.Hosting = nil
	p.outer.setNeed(v, false)
	if p.outer.live() != nil {
		t.Fatal("idle session kept")
	}
}

func TestPeerProviderGoneRevokes(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	vis, _ := p.outer.Get(ctx)
	r := linkReply(t, ctx, vis)
	_ = p.provider.Close()
	waitUntil(t, "link dropped", func() bool { return p.a.linkOf(r.LinkID) == nil })
}

func TestPeerServerExit(t *testing.T) {
	p := startPeer(t, true)
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: p.echo, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	checkEcho(t, c, "exit")
	c2, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	checkEcho(t, c2, "no target")
	if got := p.exitTarget.Load(); got == nil || *got != "" {
		t.Fatalf("server exit got target %v", got)
	}
	s, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := wire.AppendStreamOpen(&wire.StreamOpen{Op: wire.OpTCP, ProviderTarget: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(info)
	_ = st.Flush()
	wantReset(t, st, wire.CodeMalformed)
	if p.punch.punch.Load() != 0 {
		t.Fatal("server exit punched")
	}
}

// concurrentEcho runs six streams at once, checks every byte, and returns the
// most outbound lanes a pipe had.
func concurrentEcho(t *testing.T, ctx context.Context, v *Visitor) int {
	t.Helper()
	var most atomic.Int64
	stop := make(chan struct{})
	var watch sync.WaitGroup
	watch.Go(func() {
		for {
			if l := v.live(); l != nil {
				for _, s := range l.zc.PipeStats() {
					if int64(s.LanesOut) > most.Load() {
						most.Store(int64(s.LanesOut))
					}
				}
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	var wg sync.WaitGroup
	for i := range 6 {
		c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		data := bytes.Repeat([]byte{byte(i + 1), byte(i * 3), 7}, 4<<20/3)
		wg.Go(func() {
			if _, err := c.Write(data); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			got := make([]byte, len(data))
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, data) {
				t.Errorf("stream %d: corrupted or short: %v", i, err)
			}
		})
	}
	wg.Wait()
	close(stop)
	watch.Wait()
	return int(most.Load())
}

// Bound on the direct path: concurrent streams spread over several lanes,
// within session_max_lanes.
func TestPeerLanesOnDirect(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "x")
	n := concurrentEcho(t, ctx, v)
	t.Logf("lanes out: %d", n)
	if n < 2 || n > DefaultMaxLanes {
		t.Fatalf("%d lanes", n)
	}
	for _, s := range v.live().zc.PipeStats() {
		if s.RecordMode != zmux.Bound || s.Carrier != zmux.CarrierQUIC {
			t.Fatalf("pipe %+v", s.RecordMode)
		}
	}
}

func TestPeerLanesCapped(t *testing.T) {
	p := startPeerLanes(t, 3)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "x")
	n := concurrentEcho(t, ctx, v)
	t.Logf("lanes out: %d", n)
	if n < 2 || n > 3 {
		t.Fatalf("%d lanes, cap 3", n)
	}
}

func TestPeerRelayOneLane(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "x")
	if n := concurrentEcho(t, ctx, v); n != 1 {
		t.Fatalf("%d lanes on a relay", n)
	}
	for _, s := range v.live().zc.PipeStats() {
		if s.RecordMode != zmux.Sealed {
			t.Fatalf("relay in %v", s.RecordMode)
		}
	}
}

// secureLie declares a carrier secure that is not.
type secureLie struct{ io.WriteCloser }

func (*secureLie) CarrierSecure() bool { return true }

func (*secureLie) ExportKeyingMaterial(string, []byte, int) ([]byte, error) {
	return make([]byte, 32), nil
}

// A relay is no secure carrier: Bound is refused on it.
func TestPeerRelayRefusesBound(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	info, _ := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpPipe, LinkID: r.LinkID})
	pipe, err := vis.zc.OpenPipeStream(ctx, &zmux.PipeStreamOptions{Info: info})
	if err != nil {
		t.Fatal(err)
	}
	pipe = relayPipe(pipe)
	if zmux.IsCarrierSecure(pipe) {
		t.Fatal("relay declared secure")
	}
	handle, psk, _ := seal.DeriveHandlePSK(r.LinkKey, []byte(linkDomain), nil)
	ectx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := seal.Establish(ectx, pipe, &seal.ClientConfig{Anchor: &anchor{}, Mode: zmux.Bound, Handle: handle, PSK: psk}); err == nil {
		t.Fatal("Bound established on a relay")
	} else {
		t.Log(err)
	}
	// A visitor lying about its carrier is refused by the provider.
	r2 := linkReply(t, ctx, vis)
	info2, _ := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpPipe, LinkID: r2.LinkID})
	lying, err := vis.zc.OpenPipeStream(ctx, &zmux.PipeStreamOptions{Info: info2})
	if err != nil {
		t.Fatal(err)
	}
	lying.W = &secureLie{lying.W}
	h2, k2, _ := seal.DeriveHandlePSK(r2.LinkKey, []byte(linkDomain), nil)
	ectx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	if _, err := seal.Establish(ectx2, lying, &seal.ClientConfig{Anchor: &anchor{}, Mode: zmux.Bound, Handle: h2, PSK: k2}); err == nil {
		t.Fatal("provider took Bound on a relay")
	} else {
		t.Log(err)
	}
	if allowNestedMode(zmux.CarrierInfo{}, zmux.Bound, nil) || allowNestedMode(zmux.CarrierInfo{Secure: true}, zmux.Trusted, nil) {
		t.Fatal("record mode allowed")
	}
}

// The nested Hello tells the visitor what the provider decompresses (§9.3).
func TestPeerNestedCompression(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnRelay, 0)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true, Compress: []string{"flate"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*linkConn); ok {
		t.Fatal("stream was not compressed")
	}
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "hello" {
		t.Fatalf("%q, %v", b, err)
	}
}
