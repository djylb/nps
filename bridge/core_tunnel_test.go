package bridge

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/client"
	legacyclient "github.com/djylb/nps/client/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
)

type dbClients struct{ db *file.DbUtils }

func (s dbClients) RangeClients(fn func(coreapi.ClientRecord) bool) {
	s.db.JsonDb.Clients.Range(func(_, v any) bool {
		if r, ok := CoreRecord(v.(*model.Client)); ok {
			return fn(r)
		}
		return true
	})
}

type coreFixture struct {
	db     *file.DbUtils
	b      *Bridge
	a      *session.Acceptor
	client *model.Client
	extra  []*model.Client // the clients of newCoreFixtureWith
	addr   string
}

func newCoreFixture(tb testing.TB, mode SelectMode, cnf model.Config) *coreFixture {
	tb.Helper()
	return newCoreFixtureOpts(tb, mode, cnf, session.Options{})
}

func newCoreFixtureOpts(tb testing.TB, mode SelectMode, cnf model.Config, opts session.Options) *coreFixture {
	tb.Helper()
	return newCoreFixtureWith(tb, mode, cnf, opts)
}

// newCoreFixtureWith is newCoreFixtureOpts with more clients, one for each
// vkey given.
func newCoreFixtureWith(tb testing.TB, mode SelectMode, cnf model.Config, opts session.Options, vkeys ...string) *coreFixture {
	tb.Helper()
	root := tb.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		tb.Fatal(err)
	}
	db := file.New(root)
	c := model.NewClient("core-vkey", false, false)
	c.Status = true
	if err := db.NewClient(c); err != nil {
		tb.Fatal(err)
	}
	c.Cnf.Crypt, c.Cnf.Compress, c.Cnf.CompressAlg = cnf.Crypt, cnf.Compress, cnf.CompressAlg
	var extra []*model.Client
	for _, vkey := range vkeys {
		x := model.NewClient(vkey, false, false)
		x.Status = true
		if err := db.NewClient(x); err != nil {
			tb.Fatal(err)
		}
		extra = append(extra, x)
	}
	a, err := session.NewAcceptor(dbClients{db}, opts)
	if err != nil {
		tb.Fatal(err)
	}
	b := NewTunnel(db, &sync.Map{}, Options{DisconnectTime: 30, SelectMode: mode, CoreEntry: a})
	if err := b.startCore(); err != nil {
		tb.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.serveCarrier(c, "tcp")
		}
	})
	tb.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
		_ = b.Close()
	})
	return &coreFixture{db: db, b: b, a: a, client: c, extra: extra, addr: ln.Addr().String()}
}

// followClients keeps the entry current with the store, as nps does.
func (f *coreFixture) followClients() {
	f.db.OnClientChanged(func(c *model.Client, deleted bool) {
		if r, ok := CoreRecord(c); ok && !deleted {
			f.a.ClientChanged(r)
		} else {
			f.a.ClientRemoved(c.Id)
		}
	})
}

// joinCore connects one npc on the new protocol, a process of its own.
func (f *coreFixture) joinCore(tb testing.TB, exit coreapi.Exit) coreapi.ClientSession {
	tb.Helper()
	cands, err := connprofile.Parse(f.addr, "tcp")
	if err != nil {
		tb.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "npc/9.8.7 test/x",
		Key: key, Exit: exit, Timeout: 4 * time.Second}).Dial(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close(); <-s.Done() })
	return s
}

// joinOld connects a real npc of the old protocol.
// joinCoreTrusted reconnects so that the record=4 of the first CONFIG applies.
func (f *coreFixture) joinCoreTrusted(tb testing.TB) {
	tb.Helper()
	cands, err := connprofile.Parse(f.addr, "tcp")
	if err != nil {
		tb.Fatal(err)
	}
	d := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "npc/9.8.7 test/x",
		Exit: client.NewCoreExit("", 0), Timeout: 4 * time.Second})
	first, err := d.Dial(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	_ = first.Close()
	<-first.Done()
	s, err := d.Dial(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close(); <-s.Done() })
}

func (f *coreFixture) joinOld(tb testing.TB) {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		legacyclient.New(f.addr, f.client.VerifyKey, "tcp", 30).Start(ctx)
	}()
	tb.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			tb.Error("old client did not stop")
		}
	})
}

func (f *coreFixture) waitConnected(tb testing.TB) {
	tb.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if v, ok := f.b.Client.Load(f.client.Id); ok {
			c := v.(*Client)
			if n := c.CheckNode(); n != nil && n.path() != nil {
				return
			}
		}
	}
	tb.Fatal("client not connected")
}

func echoListener(tb testing.TB) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			})
		}
	})
	tb.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return ln.Addr().String()
}

func (f *coreFixture) startTunnel(tb testing.TB, target string, override string) (string, *model.Tunnel) {
	tb.Helper()
	task := &model.Tunnel{Mode: "tcp", ServerIp: "127.0.0.1", Port: 0, Client: f.client, Flow: &model.Flow{}, Compress: override,
		Target: &model.Target{TargetStr: target}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	task.Port = l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	srv := proxy.NewTunnelModeServer(proxy.ProcessTunnel, f.b, task, false)
	if err := srv.Bind(); err != nil {
		tb.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Start() }()
	tb.Cleanup(func() { _ = srv.Close(); <-done })
	return l.Addr().String(), task
}

// visit sends size bytes through the tunnel and reads the echo.
func visit(tb testing.TB, addr string, size int) {
	tb.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	payload := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	werr := make(chan error, 1)
	go func() { _, err := c.Write(payload); werr <- err }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		tb.Fatal(err)
	}
	if err := <-werr; err != nil {
		tb.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		tb.Fatal("echo differs")
	}
}

// Both protocols carry a tunnel through the same code and count the same.
func TestTunnelEndToEndAndFlow(t *testing.T) {
	const size = 1 << 20
	for _, tc := range []struct {
		name string
		cnf  model.Config
		join func(*coreFixture, testing.TB)
	}{
		{"old", model.Config{}, func(f *coreFixture, tb testing.TB) { f.joinOld(tb) }},
		{"old compressed", model.Config{Compress: true}, func(f *coreFixture, tb testing.TB) { f.joinOld(tb) }},
		{"new", model.Config{}, func(f *coreFixture, tb testing.TB) { f.joinCore(tb, client.NewCoreExit("", 0)) }},
		{"new compressed", model.Config{Compress: true, CompressAlg: "zstd"}, func(f *coreFixture, tb testing.TB) { f.joinCore(tb, client.NewCoreExit("", 0)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoreFixture(t, Primary, tc.cnf)
			tc.join(f, t)
			f.waitConnected(t)
			addr, task := f.startTunnel(t, echoListener(t), model.CompressDefault)
			visit(t, addr, size)
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
				if in, out := task.Flow.Traffic().Total(); in == size && out == size {
					return
				}
			}
			in, out := task.Flow.Traffic().Total()
			t.Fatalf("flow in=%d out=%d, want %d", in, out, size)
		})
	}
}

type countExit struct {
	coreapi.Exit
	n atomic.Int64
}

func (e *countExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	e.n.Add(1)
	return e.Exit.Dial(ctx, network, target)
}

func TestSelectionAcrossSessionsAndProtocols(t *testing.T) {
	f := newCoreFixture(t, RoundRobin, model.Config{})
	a, b := &countExit{Exit: client.NewCoreExit("", 0)}, &countExit{Exit: client.NewCoreExit("", 0)}
	f.joinCore(t, a)
	f.joinCore(t, b)
	old := joinRouteNode(t, f.b, f.client.Id, "old-node")
	target := echoListener(t)
	v, _ := f.b.Client.Load(f.client.Id)
	// A session is the client's before the server has registered it.
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end) && v.(*Client).NodeCount() != 3; time.Sleep(5 * time.Millisecond) {
	}
	if n := v.(*Client).NodeCount(); n != 3 {
		t.Fatalf("nodes: %d", n)
	}
	for range 6 {
		c, err := f.b.Session(f.client.Id).OpenLink(conn.NewLink(conn.LinkTCP, target, false, false, "1.1.1.1:1", false), 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
	}
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end) && (a.n.Load() < 2 || b.n.Load() < 2 || len(old.links) < 2); time.Sleep(5 * time.Millisecond) {
	}
	if a.n.Load() != 2 || b.n.Load() != 2 || len(old.links) != 2 {
		t.Fatalf("spread: %d %d old %d", a.n.Load(), b.n.Load(), len(old.links))
	}
}

func TestClientDisabledMidSession(t *testing.T) {
	f := newCoreFixture(t, Primary, model.Config{})
	s := f.joinCore(t, client.NewCoreExit("", 0))
	f.waitConnected(t)
	f.client.Status = false
	f.b.DelClient(f.client.Id)
	select {
	case <-s.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("session not closed")
	}
	var ae *zmux.ApplicationError
	if !errors.As(s.Err(), &ae) || uint64(ae.ErrorCode) != 0x4e500003 {
		t.Fatalf("not NPS_AUTH: %v", s.Err())
	}
	if f.b.Connected(f.client.Id) {
		t.Fatal("still connected")
	}
}

// client_online reads the version of the node that came online: the stored
// one is only refreshed by the periodic sampler.
func TestSessionVersionOfOldNode(t *testing.T) {
	n := NewNode("u", "0.34.1", legacy.LatestIndex)
	if got := sessionVersion(NewClient(1, n, Primary)); got != "0.34.1" {
		t.Fatalf("%q", got)
	}
	if got := sessionVersion(nil); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestCoreOnlineIsAClientOnline(t *testing.T) {
	f := newCoreFixture(t, Primary, model.Config{})
	f.joinCore(t, client.NewCoreExit("", 0))
	f.waitConnected(t)
	v, _ := f.b.Client.Load(f.client.Id)
	c := v.(*Client)
	n, ok := c.node(c.lastUUID())
	if !ok {
		t.Fatal("no node")
	}
	if _, ver := n.versions(); ver != "9.8.7" {
		t.Fatalf("version %q", ver)
	}
	f.client.RLock()
	defer f.client.RUnlock()
	if f.client.Mode != "tcp" || f.client.Addr != "127.0.0.1" {
		t.Fatalf("mode %q addr %q", f.client.Mode, f.client.Addr)
	}
}

func TestCorePingAndUDPThroughOpenLink(t *testing.T) {
	f := newCoreFixture(t, Primary, model.Config{})
	f.joinCore(t, client.NewCoreExit("", 0))
	f.waitConnected(t)
	ping := conn.NewLink(conn.LinkPing, "", false, false, "", false)
	ping.Option.NeedAck = true
	c, err := f.b.Session(f.client.Id).OpenLink(ping, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	go func() {
		b := make([]byte, 2048)
		n, a, err := pc.ReadFrom(b)
		if err == nil {
			_, _ = pc.WriteTo(b[:n], a)
		}
	}()
	link := conn.NewLink(conn.LinkUDP, pc.LocalAddr().String(), true, true, "1.1.1.1:1", false)
	c, err = f.b.Session(f.client.Id).OpenLink(link, 0)
	if err != nil {
		t.Fatal(err)
	}
	if link.Crypt || link.Compress {
		t.Fatal("the session encrypts and compresses by itself")
	}
	fr := netx.NewFramedConn(c, netx.WithFramedDatagrams())
	defer func() { _ = fr.Close() }()
	_ = fr.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := fr.Write([]byte("udp")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	if n, err := fr.Read(b); err != nil || string(b[:n]) != "udp" {
		t.Fatalf("%q %v", b[:n], err)
	}
}

// fallbackRec answers with what it read of the request.
func (fallbackRec) ServeHTTP3(*quic.Conn, func(http.Handler) http.Handler) bool { return false }

type fallbackRec struct{ got chan string }

func (f fallbackRec) ServeFallback(c net.Conn, _ *tls.ConnectionState) bool {
	go func() {
		defer func() { _ = c.Close() }()
		b := make([]byte, 4096)
		n, _ := c.Read(b)
		f.got <- string(b[:n])
		_, _ = c.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	}()
	return true
}

func (fallbackRec) FallbackCert(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }

func TestWSPortUnknownPathGoesToFallback(t *testing.T) {
	f := newCoreFixture(t, Primary, model.Config{})
	rec := fallbackRec{got: make(chan string, 1)}
	f.b.SetFallback(rec)
	base := conn.NewVirtualListener(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _ = conn.Accept(ln, func(c net.Conn) { conn.ServeVirtual(base, c) }) })
	wsl := conn.NewWSListener(base, "/ws", "", "", f.b.coreWS())
	t.Cleanup(func() { _ = wsl.Close(); _ = ln.Close(); _ = base.Close(); wg.Wait() })
	hc := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := hc.Get("http://" + ln.Addr().String() + "/unknown/path?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := <-rec.got
	if !strings.HasPrefix(got, "GET /unknown/path?x=1 HTTP/1.1\r\n") || !strings.Contains(got, "Host: ") {
		t.Fatalf("request %q", got)
	}
}

func BenchmarkTunnel(b *testing.B) {
	for _, tc := range []struct {
		name string
		join func(*coreFixture, testing.TB)
		cnf  model.Config
	}{
		{"old", func(f *coreFixture, tb testing.TB) { f.joinOld(tb) }, model.Config{}},
		{"new_sealed", func(f *coreFixture, tb testing.TB) { f.joinCore(tb, client.NewCoreExit("", 0)) }, model.Config{Crypt: true}},
		{"new_trusted", func(f *coreFixture, tb testing.TB) { f.joinCoreTrusted(tb) }, model.Config{}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			f := newCoreFixture(b, Primary, tc.cnf)
			tc.join(f, b)
			f.waitConnected(b)
			addr, _ := f.startTunnel(b, echoListener(b), model.CompressDefault)
			const chunk = 1 << 16
			c, err := net.Dial("tcp", addr)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(60 * time.Second))
			buf := make([]byte, chunk)
			rd := make([]byte, chunk)
			b.SetBytes(chunk)
			b.ResetTimer()
			for b.Loop() {
				if _, err := c.Write(buf); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(c, rd); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// bannerListener is a server-first target: it speaks on accept, then echoes.
func bannerListener(tb testing.TB, banner string) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = c.Close() }()
				_, _ = io.WriteString(c, banner)
				_, _ = io.Copy(c, c)
			})
		}
	})
	tb.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return ln.Addr().String()
}

// A server-first target works through a tunnel: the visitor sends nothing
// until it has read the banner, which must not wait on a first client byte.
func TestTunnelServerFirstBanner(t *testing.T) {
	const banner = "SSH-2.0-test\r\n"
	for _, tc := range []struct {
		name string
		cnf  model.Config
	}{
		{"plain", model.Config{}},
		{"compress and crypt", model.Config{Crypt: true, Compress: true, CompressAlg: "zstd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoreFixture(t, Primary, tc.cnf)
			f.joinCore(t, client.NewCoreExit("", 0))
			f.waitConnected(t)
			addr, _ := f.startTunnel(t, bannerListener(t, banner), model.CompressDefault)
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			got := make([]byte, len(banner))
			if _, err := io.ReadFull(c, got); err != nil || string(got) != banner {
				t.Fatalf("banner %q, %v", got, err)
			}
			if _, err := c.Write([]byte("ping")); err != nil {
				t.Fatal(err)
			}
			echo := make([]byte, 4)
			if _, err := io.ReadFull(c, echo); err != nil || string(echo) != "ping" {
				t.Fatalf("echo %q, %v", echo, err)
			}
		})
	}
}
