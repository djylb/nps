package bridge

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/client"
	legacyclient "github.com/djylb/nps/client/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/instancekey"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/httpproxy"
)

const routeHost = "route.test"

// protoStore records the HTTP version of the requests of the proxy.
type protoStore struct {
	httpproxy.Store
	major chan int
}

func (s protoStore) GetInfoByHost(h string, r *http.Request) (*model.Host, error) {
	if r.Method == http.MethodPost {
		select {
		case s.major <- r.ProtoMajor:
		default:
		}
	}
	return s.Store.GetInfoByHost(h, r)
}

type domainFixture struct {
	*mixFixture
	http, https string
	h3          string
	store       protoStore
}

func routeFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func writeRouteCert(t *testing.T, c tls.Certificate) (string, string) {
	t.Helper()
	d := t.TempDir()
	cf, kf := filepath.Join(d, "c.pem"), filepath.Join(d, "k.pem")
	key, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kf, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cf, kf
}

// routeOn starts the real domain proxy with one route of h to target.
func domainRouteOn(t *testing.T, target string, cnf model.Config, h *model.Host) *domainFixture {
	return domainRoute(t, target, cnf, h, false)
}

func domainRoute(t *testing.T, target string, cnf model.Config, h *model.Host, virtual bool) *domainFixture {
	t.Helper()
	httpP, httpsP, h3P := routeFreePort(t), routeFreePort(t), routeFreePort(t)
	n := servercfg.NetworkConfig{BridgePath: "/ws", MixLegacy: true, HTTPProxyIP: "127.0.0.1", HTTPProxyPort: httpP, HTTPSProxyPort: httpsP, HTTP3ProxyPort: h3P}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	f := mixOnOpts(t, mixVirtual, n, cnf, session.Options{AdmitKey: key})
	b := f.b
	for _, l := range []**netx.ChanListener{&b.VirtualTcpListener, &b.VirtualTlsListener, &b.VirtualWsListener, &b.VirtualWssListener, &b.VirtualMixListener} {
		v, err := b.virtualBridgeListener(false, 0)
		if err != nil {
			t.Fatal(err)
		}
		*l = v
	}
	b.startRouteCarriers()
	b.serveVirtualTargets()

	db := f.db
	h.Id = 1
	h.Host = routeHost
	h.Client = f.client
	if virtual {
		h.Client = model.NewClient("route-owner", true, false)
		h.Client.Id = -1
		h.Client.Status = true
		b.Client.Store(h.Client.Id, NewClient(h.Client.Id, NewLocalNode("127.0.0.1", "0"), Primary))
	}
	h.Target = &model.Target{TargetStr: target, LocalProxy: true}
	h.Flow = &model.Flow{}
	h.CertFile, h.KeyFile = writeRouteCert(t, selfSigned(t, routeHost))
	if err := db.NewHost(h); err != nil {
		t.Fatal(err)
	}
	store := protoStore{Store: db, major: make(chan int, 16)}
	task := &model.Tunnel{Mode: "httpProxy", Flow: &model.Flow{}, Client: f.client, Target: &model.Target{}}
	hp := httpproxy.NewHttpProxy(store, b, task, httpP, httpsP, h3P, "", false, true, httpproxy.NewPlans())
	done := make(chan struct{})
	go func() { defer close(done); _ = hp.Start() }()
	t.Cleanup(func() { _ = hp.Close(); <-done })
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsP)))
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(end) {
			t.Fatal("proxy did not start")
		}
	}
	addr := func(p int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(p)) }
	return &domainFixture{mixFixture: f, http: addr(httpP), https: addr(httpsP), h3: addr(h3P), store: store}
}

func dialMode(tb testing.TB, f *mixFixture, server string) (coreapi.ClientSession, string) {
	tb.Helper()
	s, err := f.dialHTTP(tb, server, client.NewCoreExit("", 0), 4*time.Second)
	if err != nil {
		tb.Fatal(err)
	}
	return s, s.(interface{ RecordMode() string }).RecordMode()
}

func wantMode(t *testing.T, f *domainFixture, server, want string) {
	t.Helper()
	_, got := dialMode(t, f.mixFixture, server)
	if got != want {
		t.Fatalf("mode %s, want %s", got, want)
	}
	f.waitConnected(t)
	addr, _ := f.startTunnel(t, echoListener(t), "")
	visit(t, addr, 1<<20)
}

func params(extra string) string { return "?host=" + routeHost + "&sni=" + routeHost + extra }

func TestRouteTCPBound(t *testing.T) {
	f := domainRouteOn(t, "bridge://tcp", model.Config{Crypt: true}, &model.Host{TlsOffload: true})
	wantMode(t, f, "tls://"+f.https+"?sni="+routeHost, "bound")
}

func TestRouteWSSBound(t *testing.T) {
	for _, target := range []string{"bridge://wss", "bridge://ws"} {
		t.Run(target, func(t *testing.T) {
			f := domainRouteOn(t, target, model.Config{Crypt: true}, &model.Host{})
			wantMode(t, f, "wss://"+f.https+"/ws"+params("&subprotocol=binary"), "bound")
		})
	}
}

// A carrier takes the route's path, whatever the bridge path is.
func TestRouteHTTPCarriersOnWS(t *testing.T) {
	f := domainRouteOn(t, "bridge://ws", model.Config{Crypt: true}, &model.Host{Location: "/tun"})
	wantMode(t, f, "https://"+f.https+"/tun"+params("&kinds=streaming&http=http/1.1"), "sealed")
}

func TestRouteCDNFallsBackToSealed(t *testing.T) {
	f := domainRouteOn(t, "bridge://wss", model.Config{Crypt: true}, &model.Host{})
	cdn := tlsFront(t, f.https)
	wantMode(t, f, "wss://"+cdn+"/ws"+params("&subprotocol=binary"), "sealed")
}

// tlsFront ends TLS and opens its own to backend, as a CDN does.
func tlsFront(t *testing.T, backend string) string {
	t.Helper()
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t, "cdn.test")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				up, err := tls.Dial("tcp", backend, &tls.Config{InsecureSkipVerify: true, ServerName: routeHost, NextProtos: []string{"http/1.1"}})
				if err != nil {
					return
				}
				defer up.Close()
				go func() { _, _ = io.Copy(up, c); _ = up.CloseWrite() }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return l.Addr().String()
}

// With the switch off the next session after CONFIG is Trusted.
func TestRouteSwitchOffTrusted(t *testing.T) {
	f := domainRouteOn(t, "bridge://wss", model.Config{Crypt: false}, &model.Host{})
	cands, err := connprofile.Parse("wss://"+f.https+"/ws"+params("&subprotocol=binary"), "")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdh.X25519().GenerateKey(rand.Reader)
	d := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "t", Key: key, Exit: client.NewCoreExit("", 0), Timeout: 3 * time.Second})
	var last string
	for end := time.Now().Add(6 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		s, err := d.Dial(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		last = s.(interface{ RecordMode() string }).RecordMode()
		time.Sleep(150 * time.Millisecond)
		_ = s.Close()
		<-s.Done()
		if last == "trusted" {
			return
		}
	}
	t.Fatalf("mode %s, want trusted", last)
}

func TestRouteH2Sealed(t *testing.T) {
	f := domainRouteOn(t, "bridge://ws", model.Config{Crypt: true}, &model.Host{})
	wantMode(t, f, "https://"+f.https+"/ws"+params("&kinds=streaming&http=h2"), "sealed")
	if m := <-f.store.major; m != 2 {
		t.Fatalf("HTTP/%d", m)
	}
}

func TestRouteH3Bound(t *testing.T) {
	f := domainRouteOn(t, "bridge://ws", model.Config{Crypt: true}, &model.Host{})
	wantMode(t, f, "https://"+f.h3+"/ws"+params("&kinds=http3&http=h3"), "bound")
	if m := <-f.store.major; m != 3 {
		t.Fatalf("HTTP/%d", m)
	}
}

// A request that is no carrier gets the 404 of an unknown site, and does not
// come back to the route.
func TestRouteFallback(t *testing.T) {
	f := domainRouteOn(t, "bridge://ws", model.Config{Crypt: true}, &model.Host{})
	req, _ := http.NewRequest(http.MethodGet, "http://"+f.http+"/page", nil)
	req.Host = routeHost
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
}

// The old protocol keeps its way through the route.
func TestRouteLegacyUnchanged(t *testing.T) {
	httpRoute := domainRoute(t, "bridge://ws", model.Config{Crypt: true}, &model.Host{}, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	front := hostFront(t, httpRoute.http)
	go func() {
		defer close(done)
		legacyclient.New(front+"/ws", httpRoute.client.VerifyKey, "ws", 30).Start(ctx)
	}()
	defer func() {
		cancel()
		_ = httpRoute.b.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("old client did not stop")
		}
	}()
	httpRoute.waitConnected(t)
}

// hostFront forwards TCP to addr, rewriting the Host of the first request.
func hostFront(t *testing.T, addr string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				up, err := net.Dial("tcp", addr)
				if err != nil {
					return
				}
				defer up.Close()
				buf := make([]byte, 8192)
				n, _ := c.Read(buf)
				head := string(buf[:n])
				i := strings.Index(head, "Host: ")
				j := strings.Index(head[i:], "\r\n")
				_, _ = io.WriteString(up, head[:i]+"Host: "+routeHost+head[i+j:])
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return l.Addr().String()
}

func exchangeMix(t *testing.T) (*mixFixture, string) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return mixOnOpts(t, mixVirtual, servercfg.NetworkConfig{MixLegacy: true}, model.Config{Crypt: true}, session.Options{AdmitKey: key}), hex.EncodeToString(key)
}

func TestExchangeOnlineAndLargeFile(t *testing.T) {
	f, key := exchangeMix(t)
	_, mode := dialMode(t, f, "http://"+f.addr+"/ws?kinds=exchange&admit_key="+key)
	if mode != "sealed" {
		t.Fatal(mode)
	}
	f.waitConnected(t)
	addr, _ := f.startTunnel(t, echoListener(t), "")
	visit(t, addr, 8<<20)
}

// noUpgrade refuses WebSocket and buffers bodies whole.
func noUpgrade(t *testing.T, addr string) string {
	t.Helper()
	p := reverseProxy(t, addr, true)
	return p.URL
}

func TestExchangeThroughBufferingProxy(t *testing.T) {
	f, key := exchangeMix(t)
	p := noUpgradeProxy(t, f.addr)
	if _, err := f.dialHTTP(t, p+"/ws?kinds=streaming,split", client.NewCoreExit("", 0), 2*time.Second); err == nil {
		t.Fatal("a long request passed the buffering proxy")
	}
	s, err := f.dialHTTP(t, p+"/ws?admit_key="+key, client.NewCoreExit("", 0), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if m := s.(interface{ RecordMode() string }).RecordMode(); m != "sealed" {
		t.Fatal(m)
	}
	f.waitConnected(t)
	addr, _ := f.startTunnel(t, echoListener(t), "")
	visit(t, addr, 1<<20)
}

func noUpgradeProxy(t *testing.T, addr string) string {
	t.Helper()
	back := noUpgrade(t, addr)
	l := http.NewServeMux()
	u := strings.TrimPrefix(back, "http://")
	l.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host, r2.RequestURI = "http", u, ""
		resp, err := http.DefaultTransport.RoundTrip(r2)
		if err != nil {
			http.Error(w, "bad", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	return startHTTP(t, l)
}

func startHTTP(t *testing.T, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: h}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return "http://" + l.Addr().String()
}

func TestExchangeWrongKeyRefusedAndFallsBack(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	f, _ := exchangeMix(t)
	wrong := strings.Repeat("ab", 32)
	if _, err := f.dialHTTP(t, "http://"+f.addr+"/ws?kinds=exchange&admit_key="+wrong, client.NewCoreExit("", 0), 2*time.Second); err == nil {
		t.Fatal("a wrong admit_key connected")
	}
	select {
	case got := <-f.fallback.got:
		if !strings.HasPrefix(got, "POST") {
			t.Fatalf("fallback got %q", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("did not reach the fallback")
	}
	f.raw(t, plainGET)
	for end := time.After(4 * time.Second); ; {
		select {
		case got := <-f.fallback.got:
			if strings.HasPrefix(got, "GET") {
				// The repeated exchange requests may still come.
				go func() {
					for {
						select {
						case <-f.fallback.got:
						case <-stop:
							return
						}
					}
				}()
				return
			}
		case <-end:
			t.Fatal("a plain request did not reach the fallback")
		}
	}
}

func TestAdmitKeyStableAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.key")
	a, err := instancekey.AdmitKey(path, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := instancekey.AdmitKey(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !connectionEqual(a, b) || len(a) != 32 {
		t.Fatal("admit key changed")
	}
	over := strings.Repeat("0f", 32)
	c, err := instancekey.AdmitKey(path, over)
	if err != nil || hex.EncodeToString(c) != over {
		t.Fatal(c, err)
	}
	if _, err := instancekey.AdmitKey(path, "short"); err == nil {
		t.Fatal("a bad override was taken")
	}
}

func connectionEqual(a, b []byte) bool { return string(a) == string(b) }

// type=auto connects over TLS on a mixed port, with or without a path.
func TestAutoOnMixedPort(t *testing.T) {
	for name, server := range map[string]func(string) string{
		"no path": func(a string) string { return "auto://" + a },
		"path":    func(a string) string { return "auto://" + a + "/ws" },
	} {
		t.Run(name, func(t *testing.T) {
			f, key := exchangeMix(t)
			s, mode := dialMode(t, f, server(f.addr))
			if mode != "bound" || s.Carrier() != "tls" {
				t.Fatal(mode, s.Carrier())
			}
			_ = key
			f.waitConnected(t)
			addr, _ := f.startTunnel(t, echoListener(t), "")
			visit(t, addr, 1<<20)
		})
	}
}
