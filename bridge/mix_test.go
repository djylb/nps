package bridge

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	legacyclient "github.com/djylb/nps/client/legacy"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
)

// recordFallback records what reached the domain proxy.
type recordFallback struct {
	stubFallback
	got chan string
	wg  *sync.WaitGroup
}

func (f recordFallback) ServeFallback(c net.Conn, _ *tls.ConnectionState) bool {
	f.wg.Go(func() {
		defer func() { _ = c.Close() }()
		b := make([]byte, 64)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := c.Read(b)
		f.got <- string(b[:n])
	})
	return true
}

type mixFixture struct {
	*coreFixture
	fallback recordFallback
}

// mixOn starts a mixed entry of kind over a fixture.
func mixOn(t *testing.T, kind mixKind, n servercfg.NetworkConfig) *mixFixture {
	t.Helper()
	return mixOnOpts(t, kind, n, model.Config{Crypt: true}, session.Options{})
}

func mixOnOpts(t *testing.T, kind mixKind, n servercfg.NetworkConfig, cnf model.Config, opts session.Options) *mixFixture {
	t.Helper()
	if n.BridgePath == "" {
		n.BridgePath = "/ws"
	}
	n.QUICKeepAlivePeriod, n.QUICMaxIdleTimeout, n.QUICMaxIncomingStreams = 10, 30, 1000
	if len(n.QUICALPNList) == 0 {
		n.QUICALPNList = []string{"nps"}
	}
	if len(n.NewProtocolQUICALPN) == 0 {
		n.NewProtocolQUICALPN = []string{"h3"}
	}
	old := connection.Network()
	connection.Configure(n)
	t.Cleanup(func() { connection.Configure(old) })
	f := newCoreFixtureOpts(t, SelectMode(0), cnf, opts)
	f.b.cert = selfSigned(t, "bridge.test")
	f.b.fingerprint = crypt.GetCertFingerprint(f.b.cert)
	var wg sync.WaitGroup
	rf := recordFallback{got: make(chan string, 4), wg: &wg}
	f.b.SetFallback(rf)
	t.Cleanup(wg.Wait)
	e := f.b.newMixEntry(kind)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accept sync.WaitGroup
	accept.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.b.serveMix(c, e)
		}
	})
	t.Cleanup(func() { _ = ln.Close(); accept.Wait() })
	f.addr = ln.Addr().String()
	return &mixFixture{coreFixture: f, fallback: rf}
}

func (f *mixFixture) dialCore(t *testing.T, server string, want bool) {
	t.Helper()
	carrier := "tcp"
	if t, s, ok := strings.Cut(server, "|"); ok {
		carrier, server = t, s
	}
	cands, err := connprofile.Parse(server, carrier)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdh.X25519().GenerateKey(rand.Reader)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "t", Key: key, Timeout: 1500 * time.Millisecond}).Dial(ctx)
	if err == nil {
		_ = s.Close()
		<-s.Done()
	}
	if (err == nil) != want {
		t.Fatalf("new protocol on %s: err %v, want success %v", server, err, want)
	}
}

func (f *mixFixture) dialOld(t *testing.T, carrier string, want bool) {
	t.Helper()
	addr := f.addr
	if carrier == "ws" || carrier == "wss" {
		addr += "/ws"
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		legacyclient.New(addr, f.client.VerifyKey, carrier, 30).Start(ctx)
	}()
	defer func() {
		cancel()
		_ = f.b.Close() // ends a stalled handshake of the client
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("old client did not stop")
		}
	}()
	connected := false
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end) && !connected; time.Sleep(10 * time.Millisecond) {
		if v, ok := f.b.Client.Load(f.client.Id); ok {
			if n := v.(*Client).CheckNode(); n != nil && n.path() != nil {
				connected = true
			}
		}
	}
	if connected != want {
		t.Fatalf("old protocol over %s: connected %v, want %v", carrier, connected, want)
	}
}

func (f *mixFixture) raw(t *testing.T, payload string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	return c
}

const plainGET = "GET / HTTP/1.1\r\nHost: x\r\n\r\n"

func (f *mixFixture) wantHTTPFallback(t *testing.T) {
	t.Helper()
	f.raw(t, plainGET)
	select {
	case got := <-f.fallback.got:
		if !strings.HasPrefix(got, "GET") {
			t.Fatalf("fallback got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("http did not reach the domain proxy")
	}
}

func (f *mixFixture) wantStall(t *testing.T, payload string) {
	t.Helper()
	c := f.raw(t, payload)
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var b [1]byte
	_, err := c.Read(b[:])
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("want a stall, got %v", err)
	}
}

func TestMixPortPlainOff(t *testing.T) {
	f := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true})
	t.Run("tls new", func(t *testing.T) { f.dialCore(t, "tls://"+f.addr, true) })
	t.Run("wss new", func(t *testing.T) { f.dialCore(t, "wss://"+f.addr+"/ws", true) })
	t.Run("tcp new off", func(t *testing.T) { f.dialCore(t, "tcp://"+f.addr, false) })
	t.Run("ws new off", func(t *testing.T) { f.dialCore(t, "ws://"+f.addr+"/ws", false) })
	t.Run("http to proxy", func(t *testing.T) { f.wantHTTPFallback(t) })
	t.Run("junk stalls", func(t *testing.T) { f.wantStall(t, "xyzzy") })
	t.Run("plain TST stalls", func(t *testing.T) { f.wantStall(t, "TST!!") })
	t.Run("old tls", func(t *testing.T) {
		g := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true})
		g.dialOld(t, "tls", true)
	})
	t.Run("old tcp off", func(t *testing.T) {
		g := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true})
		g.dialOld(t, "tcp", false)
	})
	t.Run("old wss off", func(t *testing.T) {
		g := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true})
		g.dialOld(t, "wss", false)
	})
	t.Run("mix_legacy off", func(t *testing.T) {
		g := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: false})
		g.dialOld(t, "tls", false)
	})
}

func TestMixPortPlainOn(t *testing.T) {
	n := servercfg.NetworkConfig{MixLegacy: true, MixPlain: true}
	f := mixOn(t, mixPort, n)
	t.Run("tcp new", func(t *testing.T) { f.dialCore(t, "tcp://"+f.addr, true) })
	t.Run("ws new", func(t *testing.T) { f.dialCore(t, "ws://"+f.addr+"/ws", true) })
	t.Run("ws binary new", func(t *testing.T) { f.dialCore(t, "ws|"+f.addr+"/ws", true) })
	t.Run("http to proxy", func(t *testing.T) { f.wantHTTPFallback(t) })
	t.Run("junk stalls", func(t *testing.T) { f.wantStall(t, "xyzzy") })
	t.Run("old tcp", func(t *testing.T) { mixOn(t, mixPort, n).dialOld(t, "tcp", true) })
	t.Run("old ws off", func(t *testing.T) { mixOn(t, mixPort, n).dialOld(t, "ws", false) })
}

func TestMixVirtualTarget(t *testing.T) {
	// mix_plain stays off: the virtual target does not read it.
	n := servercfg.NetworkConfig{MixLegacy: true}
	f := mixOn(t, mixVirtual, n)
	for _, s := range []string{"tcp://", "tls://", "ws://", "wss://"} {
		u := s + f.addr
		if strings.HasPrefix(s, "ws") {
			u += "/ws"
		}
		t.Run(s, func(t *testing.T) { f.dialCore(t, u, true) })
	}
	for carrier, want := range map[string]bool{"tcp": true, "tls": true, "ws": false, "wss": false} {
		t.Run("old "+carrier, func(t *testing.T) { mixOn(t, mixVirtual, n).dialOld(t, carrier, want) })
	}
}

func TestGenericBridgePort(t *testing.T) {
	n := servercfg.NetworkConfig{NewProtocolWSPaths: []string{"/new"}}
	f := mixOn(t, mixGeneric, n)
	t.Run("tcp new", func(t *testing.T) { f.dialCore(t, "tcp://"+f.addr, true) })
	t.Run("tls new", func(t *testing.T) { f.dialCore(t, "tls://"+f.addr, true) })
	t.Run("ws binary new", func(t *testing.T) { f.dialCore(t, "ws|"+f.addr+"/ws", true) })
	t.Run("wss binary new", func(t *testing.T) { f.dialCore(t, "wss|"+f.addr+"/ws", true) })
	t.Run("ws path new", func(t *testing.T) { f.dialCore(t, "ws://"+f.addr+"/new", true) })
	t.Run("http to proxy", func(t *testing.T) { f.wantHTTPFallback(t) })
	for _, carrier := range []string{"tcp", "tls", "ws", "wss"} {
		t.Run("old "+carrier, func(t *testing.T) { mixOn(t, mixGeneric, n).dialOld(t, carrier, true) })
	}
}

func freeUDP(t *testing.T) string {
	t.Helper()
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return "127.0.0.1:" + strconv.Itoa(l.LocalAddr().(*net.UDPAddr).Port)
}

// The bridge's QUIC port takes both protocols, whichever ALPN is used.
func TestBridgeQUICBothProtocols(t *testing.T) {
	boot := func(t *testing.T, mix bool) *mixFixture {
		f := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true})
		f.addr = freeUDP(t)
		var e *mixEntry
		if mix {
			e = f.b.newMixEntry(mixPort)
		}
		if err := f.b.listenQUIC(f.addr, "quic", e); err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Run("new", func(t *testing.T) {
		f := boot(t, false)
		f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
	})
	t.Run("old", func(t *testing.T) { boot(t, false).dialOld(t, "quic", true) })
	t.Run("mix new", func(t *testing.T) {
		f := boot(t, true)
		f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
	})
	t.Run("mix old", func(t *testing.T) { boot(t, true).dialOld(t, "quic", true) })
}

// Without HTTP/3 on the port, ALPN h3 is the carrier's even when the node has
// a domain proxy that could serve it.
func TestMixQUICH3IsCarrierWithoutHTTP3(t *testing.T) {
	f := mixOn(t, mixPort, servercfg.NetworkConfig{MixLegacy: true, MixPort: 9443, HTTP3ProxyPort: 8443})
	f.addr = freeUDP(t)
	got := make(chan struct{}, 1)
	f.b.SetFallback(h3Stub{got: got})
	if err := f.b.listenQUIC(f.addr, "mix", f.b.newMixEntry(mixPort)); err != nil {
		t.Fatal(err)
	}
	f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
	select {
	case <-got:
		t.Fatal("taken as HTTP/3")
	default:
	}
}

// With HTTP/3 on the port, h3 is HTTP/3 whatever the connection does or does
// not open; the new protocol's carrier needs another name, and a
// configuration that leaves h3 to the carrier is refused at start.
func TestMixQUICHTTP3ByALPN(t *testing.T) {
	cfg := servercfg.NetworkConfig{MixLegacy: true, MixPort: 9443, HTTP3ProxyPort: 9443, NewProtocolQUICALPN: []string{"core"}}
	boot := func(t *testing.T) (*mixFixture, chan struct{}) {
		f := mixOn(t, mixPort, cfg)
		f.addr = freeUDP(t)
		got := make(chan struct{}, 1)
		f.b.SetFallback(h3Stub{got: got})
		if err := f.b.listenQUIC(f.addr, "mix", f.b.newMixEntry(mixPort)); err != nil {
			t.Fatal(err)
		}
		return f, got
	}
	t.Run("h3 is HTTP/3 at once", func(t *testing.T) {
		f, got := boot(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		qc, err := quic.DialAddr(ctx, f.addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = qc.CloseWithError(0, "") }()
		select { // no stream is opened
		case <-got:
		case <-ctx.Done():
			t.Fatal("not taken as HTTP/3")
		}
	})
	t.Run("carriers on their own ALPN", func(t *testing.T) {
		f, got := boot(t)
		f.dialCore(t, "quic://"+f.addr+"?alpn=core", true)
		f.dialOld(t, "quic", true)
		select {
		case <-got:
			t.Fatal("carrier taken as HTTP/3")
		default:
		}
	})
	t.Run("new protocol on h3 is HTTP/3", func(t *testing.T) {
		f, _ := boot(t)
		f.dialCore(t, "quic://"+f.addr+"?alpn=h3", false)
	})
	t.Run("new protocol ALPN h3 is a configuration error", func(t *testing.T) {
		c := cfg
		c.NewProtocolQUICALPN = []string{"h3"}
		f := mixOn(t, mixPort, c)
		err := f.b.listenQUIC(freeUDP(t), "mix", f.b.newMixEntry(mixPort))
		if err == nil || !strings.Contains(err.Error(), "new_protocol_quic_alpn") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("quic_alpn h3 is a configuration error", func(t *testing.T) {
		c := cfg
		c.QUICALPN, c.QUICALPNList = "nps,h3", []string{"nps", "h3"}
		f := mixOn(t, mixPort, c)
		err := f.b.listenQUIC(freeUDP(t), "mix", f.b.newMixEntry(mixPort))
		if err == nil || !strings.Contains(err.Error(), "quic_alpn") {
			t.Fatalf("err = %v", err)
		}
	})
}

// ALPN tells the protocols apart on a QUIC port: quic_alpn is the old
// protocol's, new_protocol_quic_alpn the new one's, and a name in both is a
// configuration error.
func TestQUICProtocolByALPN(t *testing.T) {
	boot := func(t *testing.T, mix bool, n servercfg.NetworkConfig) *mixFixture {
		f := mixOn(t, mixPort, n)
		f.addr = freeUDP(t)
		var e *mixEntry
		if mix {
			e = f.b.newMixEntry(mixPort)
		}
		if err := f.b.listenQUIC(f.addr, "quic", e); err != nil {
			t.Fatal(err)
		}
		return f
	}
	for _, mix := range []bool{false, true} {
		t.Run(fmt.Sprint("mix ", mix), func(t *testing.T) {
			f := boot(t, mix, servercfg.NetworkConfig{MixLegacy: true, NewProtocolQUICALPN: []string{"h3", "core"}})
			f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
			f.dialCore(t, "quic://"+f.addr+"?alpn=core", true)
			f.dialCore(t, "quic://"+f.addr+"?alpn=nps", false) // the old protocol's name
			f.dialOld(t, "quic", true)
		})
	}
	t.Run("old protocol off on the mixed port", func(t *testing.T) {
		f := boot(t, true, servercfg.NetworkConfig{})
		f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
		f.dialOld(t, "quic", false)
	})
	t.Run("overlap", func(t *testing.T) {
		f := mixOn(t, mixPort, servercfg.NetworkConfig{NewProtocolQUICALPN: []string{"h3", "nps"}})
		err := f.b.listenQUIC(freeUDP(t), "quic", nil)
		if err == nil || !strings.Contains(err.Error(), "must not overlap") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestMixHTTP3Setting(t *testing.T) {
	for _, c := range []struct {
		mix, h3 int
		want    bool
	}{{0, 0, false}, {9443, 0, false}, {9443, 8443, false}, {9443, 9443, true}} {
		if got := (servercfg.NetworkConfig{MixPort: c.mix, HTTP3ProxyPort: c.h3}).MixHTTP3(); got != c.want {
			t.Errorf("mix %d http3 %d: %v", c.mix, c.h3, got)
		}
	}
}

type h3Stub struct {
	stubFallback
	got chan struct{}
}

func (h h3Stub) ServeHTTP3(*quic.Conn, func(http.Handler) http.Handler) bool {
	select {
	case h.got <- struct{}{}:
	default:
	}
	return true
}

// One UDP port takes the old KCP and QUIC (the KCP port of a config, whichever
// way it was written), and closing the bridge ends both listeners.
func TestBridgeKCPAndQUICSharePort(t *testing.T) {
	boot := func(t *testing.T) *mixFixture {
		f := mixOn(t, mixPort, servercfg.NetworkConfig{})
		f.addr = freeUDP(t)
		if err := f.b.listenKCP(f.addr, true); err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Run("kcp", func(t *testing.T) { boot(t).dialOld(t, "kcp", true) })
	t.Run("quic new", func(t *testing.T) {
		f := boot(t)
		f.dialCore(t, "quic://"+f.addr+"?alpn=h3", true)
	})
	t.Run("quic old", func(t *testing.T) { boot(t).dialOld(t, "quic", true) })
}
