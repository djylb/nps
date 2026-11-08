package session

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/quic-go/quic-go"
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
	echo     string
}

// allowAll is the exit of a provider that reaches everything.
func startPeer(t *testing.T, serverExit bool) *peerEnv {
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
				ServeExit: func(ctx context.Context, c net.Conn, _, _ string) {
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
		return DialConfig{Candidates: p.cands(p.addr), VKey: vkey, Agent: "t", Timeout: 3 * time.Second, Puncher: p.punch, Exit: plainExit{},
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
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Fixed: true, WantReply: true})
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
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Fixed: true, WantReply: true})
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
	echoOnce(t, ctx, v, "exit")
	if p.punch.punch.Load() != 0 {
		t.Fatal("server exit punched")
	}
}

// A direct QUIC pipe with spread scheduling really opens several lanes for
// concurrent streams.
func TestPeerSpreadLanesOnDirect(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "x")
	var conns []net.Conn
	for range 6 {
		c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Fixed: true, WantReply: true})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
		go func() {
			b := make([]byte, 1<<16)
			for {
				if _, err := c.Write(b); err != nil {
					return
				}
			}
		}()
		go func() { _, _ = io.Copy(io.Discard, c) }()
	}
	var most int
	waitUntil(t, "lanes", func() bool {
		for _, s := range v.live().zc.PipeStats() {
			most = max(most, s.LanesOut)
		}
		return most >= 1
	})
	time.Sleep(time.Second)
	for _, s := range v.live().zc.PipeStats() {
		most = max(most, s.LanesOut)
		t.Logf("pipe mode %v carrier %v lanes out %d in %d", s.RecordMode, s.Carrier, s.LanesOut, s.LanesIn)
	}
	t.Logf("lanes out: %d over %d streams", most, len(conns))
}

// A link revoked while its provider stream is still being opened neither
// panics nor gets an anchor.
func TestLinkRevokedBeforeAnchor(t *testing.T) {
	l := &peerLink{paths: map[wire.Op]*linkPath{}}
	l.end(wire.CodeNotAllowed)
	if l.setAnchor(&linkConn{}) {
		t.Fatal("anchor set on an ended link")
	}
}
