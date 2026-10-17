package client

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

// fakeRelay plays the bridge's side of a visitor tunnel: it accepts the
// manager's secret connections and hands each link to serve.
type fakeRelay struct {
	links atomic.Int32
}

// newTestManager returns a manager whose visitor tunnel ends in a fake relay
// that runs serve for every secret connection.
func newTestManager(t *testing.T, serve func(lk *conn.Link, c net.Conn)) (*P2PManager, *fakeRelay) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	clientSide, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	serverSide, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	relay := &fakeRelay{}
	server := mux.NewMux(serverSide, "tcp", 30, false)
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		for {
			mc, err := server.Accept()
			if err != nil {
				return
			}
			go func() {
				c := conn.NewConn(mc)
				defer func() { _ = c.Close() }()
				if flag, err := c.ReadFlag(); err != nil || flag != common.WorkSecret {
					return
				}
				_, _ = c.GetShortLenContent() // uuid
				_, _ = c.GetShortLenContent() // padding
				if _, err := c.GetShortContent(32); err != nil {
					return
				}
				lk, err := c.GetLinkInfo()
				if err != nil {
					return
				}
				relay.links.Add(1)
				serve(lk, c)
			}()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &P2PManager{
		ctx:        ctx,
		cancel:     cancel,
		pCancel:    cancel,
		cfg:        &config.CommonConfig{Client: &file.Client{Cnf: &file.Config{}}},
		peers:      make(map[string]*p2pPeer),
		secretConn: mux.NewMux(clientSide, "tcp", 30, true),
	}
	t.Cleanup(mgr.Close)
	return mgr, relay
}

// newTestTransport connects a QUIC transport over loopback the way the
// punched path is connected, with the pinned certificates, and returns it
// with the accepting side's connection.
func newTestTransport(t *testing.T, serverCfg *quic.Config) (*p2pTransport, *quic.Conn) {
	tr, sc, _ := newTestTransportDrop(t, serverCfg)
	return tr, sc
}

// dropConn is a PacketConn that silently drops everything while drop is
// set: a path that broke without a word, as when a NAT mapping expires.
type dropConn struct {
	net.PacketConn
	drop atomic.Bool
}

func (c *dropConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.drop.Load() {
		return len(b), nil
	}
	return c.PacketConn.WriteTo(b, addr)
}

func (c *dropConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(b)
		if err != nil || !c.drop.Load() {
			return n, addr, err
		}
	}
}

// newTestTransportDrop is newTestTransport whose visitor socket can be cut
// silently.
func newTestTransportDrop(t *testing.T, serverCfg *quic.Config) (*p2pTransport, *quic.Conn, *dropConn) {
	t.Helper()
	pin := p2pLocalPin()
	pcS, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pcC, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	trS := &quic.Transport{Conn: pcS}
	t.Cleanup(func() { _ = trS.Close(); _ = pcS.Close() })
	ln, err := trS.Listen(p2pServerTLS(pin), serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dc := &dropConn{PacketConn: pcC}
	trC := &quic.Transport{Conn: dc}
	qc, err := trC.Dial(ctx, pcS.LocalAddr(), p2pClientTLS(pin), &quic.Config{MaxIdleTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ln.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tr := newP2PTransport(qc, trC, pcC.Close)
	t.Cleanup(func() { tr.Close(p2pCodeClosed, "test") })
	return tr, sc, dc
}

// echoLink answers a relayed connection by echoing it.
func echoLink(_ *conn.Link, c net.Conn) { _, _ = io.Copy(c, c) }

func TestP2pBridgeWaitsForDirectPathWithoutRelay(t *testing.T) {
	mgr, _ := newTestManager(t, echoLink)
	b := &P2pBridge{mgr: mgr, peer: newP2PPeer(mgr, "pw", "test"), timeout: 80 * time.Millisecond}
	start := time.Now()
	_, err := b.SendLinkInfo(0, &conn.Link{}, nil)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for the P2P direct path") {
		t.Fatalf("SendLinkInfo() error = %v, want direct path timeout", err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("SendLinkInfo() returned after %s, before the bridge timeout", elapsed)
	}
}

// A direct path whose QUIC connection dies is taken out of use as soon as
// the connection reports it, and the next connection is relayed.
func TestP2PPeerDropsDeadDirectPathAndRelays(t *testing.T) {
	mgr, relay := newTestManager(t, echoLink)
	peer := newP2PPeer(mgr, "pw", "test")
	tr, remote := newTestTransport(t, &quic.Config{MaxIdleTimeout: 5 * time.Second})
	held := make(chan struct{})
	go func() {
		peer.hold(tr)
		close(held)
	}()
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if peer.waitActive(waitCtx) != tr {
		t.Fatal("transport was not installed")
	}

	_ = remote.CloseWithError(0, "gone")
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("dead direct path was not noticed within 1s")
	}
	if peer.active() != nil {
		t.Fatal("dead direct path still in use")
	}

	b := &P2pBridge{mgr: mgr, local: &config.LocalServer{Password: "pw"}, peer: peer, secret: true, timeout: time.Second}
	c, err := b.SendLinkInfo(0, conn.NewLink("tcp", "127.0.0.1:1", false, false, "", false), nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() = %v, want relayed connection", err)
	}
	defer func() { _ = c.Close() }()
	assertEcho(t, c, "relayed")
	if n := relay.links.Load(); n != 1 {
		t.Fatalf("relay received %d links, want 1", n)
	}
}

// A connection whose stream cannot be opened on the direct path within
// p2pOpenStreamTimeout goes over the relay instead: the link reaches the
// relay once, the direct path carries no stream, and the data follows the
// link.
func TestP2pBridgeRelaysWhenDirectStreamCannotOpen(t *testing.T) {
	mgr, relay := newTestManager(t, echoLink)
	peer := newP2PPeer(mgr, "pw", "test")
	// The provider allows no streams: opening one blocks until the timeout.
	tr, remote := newTestTransport(t, &quic.Config{MaxIdleTimeout: 5 * time.Second, MaxIncomingStreams: -1})
	peer.install(tr)
	accepted := make(chan struct{})
	go func() {
		if _, err := remote.AcceptStream(context.Background()); err == nil {
			close(accepted)
		}
	}()

	b := &P2pBridge{mgr: mgr, local: &config.LocalServer{Password: "pw"}, peer: peer, secret: true, timeout: time.Second}
	start := time.Now()
	c, err := b.SendLinkInfo(0, conn.NewLink("tcp", "127.0.0.1:1", false, false, "", false), nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() = %v, want relayed connection", err)
	}
	defer func() { _ = c.Close() }()
	if elapsed := time.Since(start); elapsed > p2pOpenStreamTimeout+time.Second {
		t.Fatalf("fallback took %s", elapsed)
	}
	assertEcho(t, c, "early data")
	if n := relay.links.Load(); n != 1 {
		t.Fatalf("relay received %d links, want 1", n)
	}
	if peer.active() != nil {
		t.Fatal("direct path that failed a connection is still in use")
	}
	select {
	case <-accepted:
		t.Fatal("the direct path delivered a stream")
	default:
	}
}

// A path that breaks silently is caught by the acknowledgement a new
// connection asks for after p2pIdleProbeAfter without an answer: the
// connection is relayed, and the path is closed once its checks fail.
func TestP2pBridgeRelaysOverSilentlyBrokenPath(t *testing.T) {
	mgr, relay := newTestManager(t, echoLink)
	peer := newP2PPeer(mgr, "pw", "test")
	tr, remote, cut := newTestTransportDrop(t, &quic.Config{MaxIdleTimeout: 30 * time.Second})
	go func() { // the provider: acknowledge what asks for it
		for {
			st, err := remote.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go func() {
				c := conn.NewConn(conn.NewQuicStreamConn(st, remote))
				if lk, err := c.GetLinkInfo(); err == nil && lk.Option.NeedAck {
					_ = conn.WriteACK(c, time.Second)
				}
				_ = c.Close()
			}()
		}
	}()
	peer.install(tr)
	b := &P2pBridge{mgr: mgr, local: &config.LocalServer{Password: "pw"}, peer: peer, secret: true, timeout: time.Second}

	// A recent answer: the connection goes direct without asking.
	c, err := b.SendLinkInfo(0, conn.NewLink("tcp", "127.0.0.1:1", false, false, "", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if n := relay.links.Load(); n != 0 {
		t.Fatalf("relay used on a live path")
	}

	cut.drop.Store(true)
	tr.lastHeard.Store(time.Now().Add(-2 * p2pIdleProbeAfter).UnixNano())
	c, err = b.SendLinkInfo(0, conn.NewLink("tcp", "127.0.0.1:1", false, false, "", false), nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() = %v, want relayed connection", err)
	}
	defer func() { _ = c.Close() }()
	assertEcho(t, c, "relayed")
	if n := relay.links.Load(); n != 1 {
		t.Fatalf("relay received %d links, want 1", n)
	}
	if peer.active() != nil {
		t.Fatal("silently broken path still used for new connections")
	}
	select {
	case <-tr.Done():
	case <-time.After(time.Duration(p2pCheckAttempts+1) * p2pCheckTimeout):
		t.Fatal("silently broken path not closed after its checks failed")
	}
}

// A path that fails one connection but answers its check stays in use.
func TestP2PPeerKeepsPathThatPassesCheck(t *testing.T) {
	mgr, _ := newTestManager(t, echoLink)
	peer := newP2PPeer(mgr, "pw", "test")
	tr, remote := newTestTransport(t, &quic.Config{MaxIdleTimeout: 30 * time.Second})
	go func() {
		for {
			st, err := remote.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go func() {
				c := conn.NewConn(conn.NewQuicStreamConn(st, remote))
				if lk, err := c.GetLinkInfo(); err == nil && lk.Option.NeedAck {
					_ = conn.WriteACK(c, time.Second)
				}
				_ = c.Close()
			}()
		}
	}()
	peer.install(tr)
	peer.suspect(tr, context.DeadlineExceeded)
	if peer.active() != nil {
		t.Fatal("suspect path used")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*p2pCheckTimeout)
	defer cancel()
	if peer.waitActive(ctx) != tr {
		t.Fatal("path that answered its check not used again")
	}
	if !tr.Alive() {
		t.Fatal("path that answered its check was closed")
	}
}

func assertEcho(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo = %q, %v; want %q", buf, err, msg)
	}
}

// A p2ps section serves HTTP proxy and SOCKS5 requests on its local port.
func TestP2psServesHTTPAndSocks5(t *testing.T) {
	old := DisableP2P
	DisableP2P = true
	t.Cleanup(func() { DisableP2P = old })
	hosts := make(chan string, 2)
	mgr, _ := newTestManager(t, func(lk *conn.Link, c net.Conn) {
		hosts <- lk.Host
		br := bufio.NewReader(c)
		if lk.Host == "web.test:80" {
			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}
			_ = req.Body.Close()
			_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
			return
		}
		_, _ = io.Copy(c, br)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := mgr.StartLocalServer(&config.LocalServer{Type: "p2ps", Port: port, Password: "pw"}); err != nil {
		t.Fatalf("StartLocalServer() = %v", err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)

	hc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hc.Close() }()
	_ = hc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(hc, "GET http://web.test/ HTTP/1.1\r\nHost: web.test\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(hc), nil)
	if err != nil {
		t.Fatalf("http proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("http proxy answered %d %q", resp.StatusCode, body)
	}
	if h := <-hosts; h != "web.test:80" {
		t.Fatalf("http proxy link host = %q", h)
	}

	sc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sc.Close() }()
	_ = sc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = sc.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err := io.ReadFull(sc, reply); err != nil || reply[1] != 0 {
		t.Fatalf("socks5 method reply %v, %v", reply, err)
	}
	_, _ = sc.Write([]byte{5, 1, 0, 1, 10, 0, 0, 1, 0, 80})
	connect := make([]byte, 10)
	if _, err := io.ReadFull(sc, connect); err != nil || connect[1] != 0 {
		t.Fatalf("socks5 connect reply %v, %v", connect, err)
	}
	assertEcho(t, sc, "socks")
	if h := <-hosts; h != "10.0.0.1:80" {
		t.Fatalf("socks5 link host = %q", h)
	}
}

func TestP2PLocalAddr(t *testing.T) {
	for in, want := range map[string]string{
		"":                "invalid IP",
		"0.0.0.0":         "invalid IP",
		"[::]":            "invalid IP",
		"[2001:db8::10]":  "2001:db8::10",
		" 192.168.1.10 ":  "192.168.1.10",
		"::ffff:10.0.0.1": "10.0.0.1",
	} {
		if got := p2pLocalAddr(in).String(); got != want {
			t.Fatalf("p2pLocalAddr(%q) = %s, want %s", in, got, want)
		}
	}
}
