package httpproxy

// Differential tests of the domain proxy: real listeners, real HTTP, HTTPS,
// HTTP/2, HTTP/3 and WebSocket clients, and real backends reached through a
// bridge that dials the target itself. They pin the behaviour of every host
// setting alone and in combination, independent of how the request path is
// built.

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/quic-go/quic-go/http3"
)

// dialBridge opens every link by dialing its target, as a client would.
type dialBridge struct {
	mu    sync.Mutex
	links []*conn.Link
	fail  error
}

func (b *dialBridge) Session(int) conn.ClientSession { return b }

func (b *dialBridge) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	b.mu.Lock()
	b.links = append(b.links, link)
	fail := b.fail
	b.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	return net.DialTimeout("tcp", link.Host, 2*time.Second)
}

func (b *dialBridge) opened() []*conn.Link {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*conn.Link(nil), b.links...)
}

func (b *dialBridge) setFail(err error) {
	b.mu.Lock()
	b.fail = err
	b.mu.Unlock()
}

func (*dialBridge) IsServer() bool                { return true }
func (*dialBridge) LinkCert() *tls.Certificate    { return &tls.Certificate{} }
func (*dialBridge) GlobalSource() *acl.Source     { return nil }
func (*dialBridge) CliProcess(*conn.Conn, string) {}

type envOptions struct {
	httpsPort  int  // the HTTPS port redirects name, the listener's if zero
	http3      bool // serve HTTP/3 too
	localProxy bool // AllowLocalProxy
}

// proxyEnv is a running domain proxy on loopback.
type proxyEnv struct {
	t         *testing.T
	db        *file.DbUtils
	bridge    *dialBridge
	hp        *HttpProxy
	client    *model.Client
	httpAddr  string
	httpsAddr string
	h3Addr    string
	cert      testCert
	nextID    int
}

const testHostCertName = "*.test"

func newProxyEnv(t *testing.T, o envOptions) *proxyEnv {
	t.Helper()
	e := &proxyEnv{t: t, db: newTestDb(t), bridge: &dialBridge{}}
	e.cert = newTestCert(t, testHostCertName)
	e.client = &model.Client{Id: 1, Cnf: &model.Config{}, Flow: &model.Flow{}}

	httpL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsInner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsL := &trackingListener{Listener: httpsInner}
	e.httpAddr, e.httpsAddr = httpL.Addr().String(), httpsL.Addr().String()
	httpsPort := o.httpsPort
	if httpsPort == 0 {
		httpsPort = httpsL.Addr().(*net.TCPAddr).Port
	}

	var udp net.PacketConn
	if o.http3 {
		udp, err = net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		e.h3Addr = udp.LocalAddr().String()
	}

	hp := NewHttpProxy(e.db, e.bridge, &model.Tunnel{Flow: &model.Flow{}, Client: e.client},
		httpL.Addr().(*net.TCPAddr).Port, httpsPort, 0, "only-pass", false, o.localProxy, NewPlans())
	if udp != nil {
		hp.Http3Port = udp.LocalAddr().(*net.UDPAddr).Port
		hp.Http3PortStr = strconv.Itoa(hp.Http3Port)
	}
	hp.ErrorContent = []byte("nps 404")
	hp.ErrorAlways = true
	hp.ResponseHeaderTimeout = 5 * time.Second
	e.hp = hp

	hs := NewHttpServer(hp, httpL)
	hp.HttpServer = hs
	https := &HttpsServer{
		HttpServer:         hs,
		httpsListener:      httpsL,
		httpsServeListener: NewHttpsListener(httpsL),
		cert:               newCertFiles(servercfg.SSLConfig{}),
		tlsNextProtos:      []string{"h2", "http/1.1"},
		ticketKeys:         [][32]byte{{1}},
	}
	https.httpsServer = https.NewServer(0, "https")
	hp.HttpsServer = https

	var wg sync.WaitGroup
	wg.Go(func() { _ = hs.Start() })
	wg.Go(func() { _ = https.httpsServer.Serve(https.httpsServeListener) })
	wg.Go(func() { _ = https.Start() })
	var h3 *Http3Server
	if udp != nil {
		h3 = NewHttp3Server(https, udp)
		hp.Http3Server = h3
		wg.Go(func() { _ = h3.Start() })
	}
	t.Cleanup(func() {
		_ = hs.Close()
		_ = httpsL.Close()
		_ = https.httpsServer.Close()
		_ = https.httpsServeListener.Close()
		if h3 != nil {
			_ = h3.Close()
		}
		wg.Wait()
		httpsL.conns.Wait()
		hp.plans.Clear()
	})
	return e
}

// addHost stores a host served by e's client and certificate; edit sets its
// settings before it is stored.
func (e *proxyEnv) addHost(name, target string, edit func(*model.Host)) *model.Host {
	e.t.Helper()
	e.nextID++
	h := &model.Host{
		Id:       e.nextID,
		Host:     name,
		Scheme:   "all",
		Client:   e.client,
		Flow:     &model.Flow{},
		Target:   &model.Target{TargetStr: target},
		CertFile: string(e.cert.certPEM),
		KeyFile:  string(e.cert.keyPEM),
	}
	if edit != nil {
		edit(h)
	}
	if err := e.db.NewHost(h); err != nil {
		e.t.Fatal(err)
	}
	return h
}

// editHost changes h as an edit does: in place, then dropping what the proxy
// cached of it.
func (e *proxyEnv) editHost(h *model.Host, edit func(*model.Host)) {
	h.Lock()
	edit(h)
	h.Unlock()
	e.hp.plans.Remove(h.Id)
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

type reply struct {
	*http.Response
	body string
}

// info decodes the request an echo backend saw.
func (r reply) info(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("backend reply %d %q: %v", r.StatusCode, r.body, err)
	}
	return m
}

func doRequest(t *testing.T, tr http.RoundTripper, req *http.Request) (reply, error) {
	t.Helper()
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: noRedirect}
	resp, err := c.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return reply{Response: resp, body: string(body)}, err
}

func newRequest(t *testing.T, method, scheme, addr, host, path string, header http.Header) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, scheme+"://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	maps.Copy(req.Header, header)
	return req
}

// get sends one request over plain HTTP on a connection of its own.
func (e *proxyEnv) get(host, path string, header http.Header) (reply, error) {
	return e.do(http.MethodGet, host, path, header)
}

func (e *proxyEnv) do(method, host, path string, header http.Header) (reply, error) {
	tr := &http.Transport{DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	return doRequest(e.t, tr, newRequest(e.t, method, "http", e.httpAddr, host, path, header))
}

func (e *proxyEnv) mustGet(host, path string, header http.Header) reply {
	e.t.Helper()
	r, err := e.get(host, path, header)
	if err != nil {
		e.t.Fatalf("GET %s%s: %v", host, path, err)
	}
	return r
}

func (e *proxyEnv) tlsConfig(host string, protos ...string) *tls.Config {
	pool := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(e.cert.der)
	if err != nil {
		e.t.Fatal(err)
	}
	pool.AddCert(leaf)
	return &tls.Config{ServerName: host, RootCAs: pool, NextProtos: protos}
}

// getTLS sends one request over HTTPS, HTTP/2 if h2 is set.
func (e *proxyEnv) getTLS(host, path string, header http.Header, h2 bool) (reply, error) {
	tr := &http.Transport{TLSClientConfig: e.tlsConfig(host), DisableKeepAlives: true, ForceAttemptHTTP2: h2}
	if !h2 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}
	defer tr.CloseIdleConnections()
	return doRequest(e.t, tr, newRequest(e.t, http.MethodGet, "https", e.httpsAddr, host, path, header))
}

func (e *proxyEnv) getH3(host, path string) (reply, error) {
	tr := &http3.Transport{TLSClientConfig: e.tlsConfig(host, http3.NextProtoH3)}
	defer func() { _ = tr.Close() }()
	return doRequest(e.t, tr, newRequest(e.t, http.MethodGet, "https", e.h3Addr, host, path, nil))
}

func basicAuth(user, pass string) http.Header {
	return http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))}}
}

// backend is an HTTP server that answers with what it received.
type backend struct {
	*httptest.Server
	addr    string
	release chan struct{} // ends requests to /slow
	entered chan struct{} // a request to /slow arrived
}

func newBackend(t *testing.T, name string, useTLS bool) *backend {
	t.Helper()
	b := &backend{release: make(chan struct{}), entered: make(chan struct{}, 8)}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws":
			b.serveUpgrade(w, r, name)
			return
		case "/ws-deny":
			w.Header().Set("X-Backend", name)
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "denied")
			return
		case "/slow":
			b.entered <- struct{}{}
			<-b.release
		case "/big":
			_, _ = w.Write(make([]byte, 96<<10))
			return
		case "/cors-own":
			w.Header().Set("Access-Control-Allow-Origin", "own")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Backend", name)
		_ = json.NewEncoder(w).Encode(requestInfo(r, name))
	})
	if useTLS {
		b.Server = httptest.NewTLSServer(h)
	} else {
		b.Server = httptest.NewServer(h)
	}
	b.addr = b.Listener.Addr().String()
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
		b.Close()
	})
	return b
}

func requestInfo(r *http.Request, name string) map[string]string {
	m := map[string]string{
		"backend": name,
		"method":  r.Method,
		"host":    r.Host,
		"path":    r.URL.Path,
		"query":   r.URL.RawQuery,
		"xff":     strings.Join(r.Header.Values("X-Forwarded-For"), ","),
		"xrip":    r.Header.Get("X-Real-IP"),
		"xfp":     r.Header.Get("X-Forwarded-Proto"),
		"xfh":     r.Header.Get("X-Forwarded-Host"),
		"xop":     r.Header.Get("X-Original-Path"),
		"origin":  r.Header.Get("Origin"),
		"xtest":   r.Header.Get("X-Test"),
		"conn":    r.Header.Get("Connection"),
		"only":    r.Header.Get("X-NPS-Http-Only"),
		"auth":    r.Header.Get("Authorization"),
		"tls":     strconv.FormatBool(r.TLS != nil),
	}
	if r.TLS != nil {
		m["sni"] = r.TLS.ServerName
	}
	return m
}

// serveUpgrade completes a WebSocket handshake, reports the request in
// response headers and echoes the stream.
func (b *backend) serveUpgrade(w http.ResponseWriter, r *http.Request, name string) {
	c, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	info := requestInfo(r, name)
	head := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
	for _, k := range []string{"backend", "host", "path", "xff", "xrip", "xfp", "xtest", "only", "tls", "sni"} {
		head += "X-Got-" + k + ": " + info[k] + "\r\n"
	}
	if _, err := io.WriteString(c, head+"\r\nhello"); err != nil {
		return
	}
	_, _ = io.Copy(c, rw)
}

// wsDial sends a WebSocket handshake for path on host over a new
// connection, TLS if useTLS is set.
func (e *proxyEnv) wsDial(host, path string, header http.Header, useTLS bool) (net.Conn, *bufio.Reader, *http.Response) {
	e.t.Helper()
	var c net.Conn
	var err error
	if useTLS {
		c, err = tls.Dial("tcp", e.httpsAddr, e.tlsConfig(host, "http/1.1"))
	} else {
		c, err = net.Dial("tcp", e.httpAddr)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	req := newRequest(e.t, http.MethodGet, "http", host, host, path, header)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	if err := req.Write(c); err != nil {
		e.t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		e.t.Fatalf("handshake response: %v", err)
	}
	return c, br, resp
}

// echoes checks that the upgraded stream greets and echoes.
func echoes(t *testing.T, c net.Conn, br *bufio.Reader) {
	t.Helper()
	got := make([]byte, 5)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "hello" {
		t.Fatalf("greeting = %q, %v", got, err)
	}
	if _, err := io.WriteString(c, "ping!"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping!" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}

func waitFlows(t *testing.T, flows ...*model.Flow) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for _, f := range flows {
		for {
			if v := f.Values(); v.InletFlow > 0 && v.ExportFlow > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("flow not counted: %+v", f.Values())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// withAddOriginHeader runs the rest of the test with http_add_origin_header on.
func withAddOriginHeader(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	on, off := filepath.Join(dir, "on.conf"), filepath.Join(dir, "off.conf")
	if err := os.WriteFile(on, []byte("http_add_origin_header=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(off, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := servercfg.Load(on); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = servercfg.Load(off) })
	if !servercfg.Current().Proxy.AddOriginHeader {
		t.Fatal("http_add_origin_header not applied")
	}
}

func TestDiffForwardPlain(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	h := e.addHost("plain.test", b.addr, nil)

	r := e.mustGet("plain.test", "/p/x?q=1", http.Header{"X-Forwarded-For": {"203.0.113.7"}})
	if r.StatusCode != 200 || r.Header.Get("X-Backend") != "a" {
		t.Fatalf("status %d, backend %q", r.StatusCode, r.Header.Get("X-Backend"))
	}
	m := r.info(t)
	if m["host"] != "plain.test" || m["path"] != "/p/x" || m["query"] != "q=1" {
		t.Fatalf("backend saw %v", m)
	}
	if m["xff"] != "203.0.113.7, 127.0.0.1" {
		t.Fatalf("X-Forwarded-For = %q", m["xff"])
	}
	// No forwarding headers unless http_add_origin_header is on.
	if m["xrip"] != "" || m["xfp"] != "" || m["xfh"] != "" || m["xop"] != "" || m["tls"] != "false" {
		t.Fatalf("backend saw %v", m)
	}
	if r.Header.Get("Alt-Svc") != "" {
		t.Fatalf("Alt-Svc without HTTP/3: %q", r.Header.Get("Alt-Svc"))
	}
	links := e.bridge.opened()
	if len(links) != 1 || links[0].Host != b.addr || links[0].ConnType != conn.LinkTCP || links[0].LocalProxy {
		t.Fatalf("links %+v", links)
	}
	if !strings.HasPrefix(links[0].RemoteAddr, "127.0.0.1:") {
		t.Fatalf("link remote address %q", links[0].RemoteAddr)
	}
	waitFlows(t, h.Flow, e.client.Flow)

	// A POST body reaches the backend.
	req := newRequest(t, http.MethodPost, "http", e.httpAddr, "plain.test", "/post", nil)
	req.Body = io.NopCloser(strings.NewReader("payload"))
	req.ContentLength = 7
	if r, err := doRequest(t, &http.Transport{DisableKeepAlives: true}, req); err != nil || r.info(t)["method"] != "POST" {
		t.Fatalf("POST: %v %v", r, err)
	}
}

func TestDiffForwardReusesBackendConnections(t *testing.T) {
	for _, compat := range []bool{false, true} {
		t.Run("compat="+strconv.FormatBool(compat), func(t *testing.T) {
			e := newProxyEnv(t, envOptions{})
			b := newBackend(t, "a", false)
			e.addHost("reuse.test", b.addr, func(h *model.Host) { h.CompatMode = compat })
			for range 3 {
				if r := e.mustGet("reuse.test", "/", nil); r.StatusCode != 200 {
					t.Fatalf("status %d", r.StatusCode)
				}
			}
			want := 1
			if compat {
				want = 3 // compatibility mode keeps no backend connection
			}
			if n := len(e.bridge.opened()); n != want {
				t.Fatalf("%d links, want %d", n, want)
			}
		})
	}
}

func TestDiffUnknownHost(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("closed.test", b.addr, func(h *model.Host) { h.IsClose = true })
	for _, name := range []string{"missing.test", "closed.test"} {
		r := e.mustGet(name, "/", nil)
		if r.StatusCode != 404 || r.body != "nps 404" || r.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %q %v", name, r.StatusCode, r.body, r.Header)
		}
		if !r.Close {
			t.Fatalf("%s: connection kept open", name)
		}
	}
	e.hp.ErrorAlways = false
	if r, err := e.get("missing.test", "/", nil); err == nil {
		t.Fatalf("unknown host answered %d without error_always", r.StatusCode)
	}
	if len(e.bridge.opened()) != 0 {
		t.Fatal("link opened for an unknown host")
	}
}

func TestDiffSourceACLDenies(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("deny.test", b.addr, func(h *model.Host) { h.BlackIpList = []string{"127.0.0.1"} })
	e.addHost("allow.test", b.addr, func(h *model.Host) { h.BlackIpList = []string{"192.0.2.1"} })
	if r, err := e.get("deny.test", "/", nil); err == nil {
		t.Fatalf("denied source answered %d", r.StatusCode)
	}
	if _, err := e.getTLS("deny.test", "/", nil, false); err == nil {
		t.Fatal("denied source answered over HTTPS")
	}
	if r := e.mustGet("allow.test", "/", nil); r.StatusCode != 200 {
		t.Fatalf("allowed source: %d", r.StatusCode)
	}
	if n := len(e.bridge.opened()); n != 1 {
		t.Fatalf("%d links, want only the allowed one", n)
	}
}

func TestDiffAutoHttps(t *testing.T) {
	for _, tc := range []struct {
		name      string
		httpsPort int
		host      string
		want      string
	}{
		{"other port", 8443, "auto.test", "https://auto.test:8443/a/b?c=d"},
		{"other port with port in host", 8443, "auto.test:8080", "https://auto.test:8443/a/b?c=d"},
		{"standard port", 443, "auto.test:8080", "https://auto.test/a/b?c=d"},
		{"standard port ipv6", 443, "[::1]:8080", "https://[::1]/a/b?c=d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newProxyEnv(t, envOptions{httpsPort: tc.httpsPort})
			b := newBackend(t, "a", false)
			name := strings.TrimSuffix(strings.TrimSuffix(tc.host, ":8080"), "]")
			name = strings.TrimPrefix(name, "[")
			e.addHost(name, b.addr, func(h *model.Host) { h.AutoHttps = true })
			r := e.mustGet(tc.host, "/a/b?c=d", nil)
			if r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != tc.want {
				t.Fatalf("got %d %q, want 301 %q", r.StatusCode, r.Header.Get("Location"), tc.want)
			}
			if len(e.bridge.opened()) != 0 {
				t.Fatal("link opened for a redirect")
			}
		})
	}

	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("auto.test", b.addr, func(h *model.Host) { h.AutoHttps = true })
	// HTTPS requests are served.
	r, err := e.getTLS("auto.test", "/", nil, false)
	if err != nil {
		t.Fatal(err)
	} else if r.StatusCode != 200 || r.info(t)["tls"] != "false" {
		t.Fatalf("HTTPS: %v %v", r.Response, err)
	}
	// So are requests with the HTTP-only password, which is not passed on.
	r = e.mustGet("auto.test", "/", http.Header{"X-Nps-Http-Only": {"only-pass"}, "X-Forwarded-For": {"203.0.113.7"}})
	if r.StatusCode != 200 {
		t.Fatalf("HTTP-only: %d", r.StatusCode)
	}
	if m := r.info(t); m["only"] != "" || m["xff"] != "" {
		t.Fatalf("HTTP-only request forwarded with %v", m)
	}
	// A wrong password is redirected like any request.
	if r := e.mustGet("auto.test", "/", http.Header{"X-Nps-Http-Only": {"wrong"}}); r.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("wrong HTTP-only password: %d", r.StatusCode)
	}
	// The upgrade of a WebSocket is redirected as well.
	_, _, resp := e.wsDial("auto.test", "/ws", nil, false)
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("WebSocket over HTTP: %d", resp.StatusCode)
	}
}

func TestDiffHttpOnlyPassword(t *testing.T) {
	withAddOriginHeader(t)
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("only.test", b.addr, func(h *model.Host) { h.HeaderChange = "X-Test: ${remote_ip}" })
	m := e.mustGet("only.test", "/", http.Header{"X-Nps-Http-Only": {"only-pass"}, "X-Forwarded-For": {"203.0.113.7"}}).info(t)
	if m["only"] != "" || m["xff"] != "" || m["xrip"] != "" || m["xfp"] != "" || m["xfh"] != "" || m["xtest"] != "127.0.0.1" {
		t.Fatalf("HTTP-only request: %v", m)
	}
	m = e.mustGet("only.test", "/", nil).info(t)
	if m["xff"] != "127.0.0.1" || m["xrip"] != "127.0.0.1" || m["xfp"] != "http" || m["xfh"] != "only.test" {
		t.Fatalf("regular request: %v", m)
	}
	// The password does not replace authentication.
	e.client.Cnf.U, e.client.Cnf.P = "u", "p"
	defer func() { e.client.Cnf.U, e.client.Cnf.P = "", "" }()
	if r := e.mustGet("only.test", "/", http.Header{"X-Nps-Http-Only": {"only-pass"}}); r.StatusCode != 401 {
		t.Fatalf("HTTP-only without credentials: %d", r.StatusCode)
	}
	// Upgrades with the password are forwarded without forwarding headers.
	c, br, resp := e.wsDial("only.test", "/ws", http.Header{"X-Nps-Http-Only": {"only-pass"}, "Authorization": basicAuth("u", "p")["Authorization"]}, false)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-only") != "" || resp.Header.Get("X-Got-xfp") != "" || resp.Header.Get("X-Got-xrip") != "" {
		t.Fatalf("WebSocket: %d %v", resp.StatusCode, resp.Header)
	}
	echoes(t, c, br)
}

func TestDiffBasicAuth(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.client.Cnf.U, e.client.Cnf.P = "user", "pass"
	e.addHost("auth.test", b.addr, func(h *model.Host) {
		h.MultiAccount = &model.MultiAccount{AccountMap: map[string]string{"multi": "m1"}}
		h.UserAuth = &model.MultiAccount{AccountMap: map[string]string{"user2": "u2"}}
	})
	for _, tc := range []struct {
		name   string
		header http.Header
		want   int
	}{
		{"none", nil, 401},
		{"wrong", basicAuth("user", "bad"), 401},
		{"client account", basicAuth("user", "pass"), 200},
		{"multi account", basicAuth("multi", "m1"), 200},
		{"user auth", basicAuth("user2", "u2"), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.mustGet("auth.test", "/", tc.header)
			if r.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", r.StatusCode, tc.want)
			}
			if tc.want == 401 {
				if r.Header.Get("WWW-Authenticate") != `Basic realm="Restricted"` || r.body != "401 Unauthorized\n" {
					t.Fatalf("401 reply %v %q", r.Header, r.body)
				}
				return
			}
			if r.info(t)["auth"] != tc.header.Get("Authorization") {
				t.Fatalf("backend saw Authorization %q", r.info(t)["auth"])
			}
		})
	}
	if n := len(e.bridge.opened()); n != 1 {
		t.Fatalf("%d links, want 1", n)
	}
	// Over HTTPS as well.
	if r, err := e.getTLS("auth.test", "/", nil, false); err != nil {
		t.Fatal(err)
	} else if r.StatusCode != 401 {
		t.Fatalf("HTTPS without credentials: %v %v", r.Response, err)
	}
	if r, err := e.getTLS("auth.test", "/", basicAuth("user", "pass"), true); err != nil {
		t.Fatal(err)
	} else if r.StatusCode != 200 || r.ProtoMajor != 2 {
		t.Fatalf("HTTP/2 with credentials: %v %v", r.Response, err)
	}
}

func TestDiffRedirectURL(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("redir.test", b.addr, func(h *model.Host) {
		h.RedirectURL = "https://target.example/${request_host}${request_path}?from=${remote_ip}&amp;x=1"
	})
	e.addHost("rewritten.test", b.addr, func(h *model.Host) {
		h.RedirectURL = "https://target.example${request_path}"
		h.Location = "/old"
		h.PathRewrite = "/new"
	})
	e.addHost("literal.test", b.addr, func(h *model.Host) { h.RedirectURL = " https://static.example/a?x=1&amp;y=2 " })

	r := e.mustGet("redir.test", "/p", nil)
	if r.StatusCode != http.StatusTemporaryRedirect || r.Header.Get("Location") != "https://target.example/redir.test/p?from=127.0.0.1&x=1" {
		t.Fatalf("got %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	// The path variable is the rewritten path.
	if r := e.mustGet("rewritten.test", "/old/x", nil); r.Header.Get("Location") != "https://target.example/new/x" {
		t.Fatalf("rewritten: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if r := e.mustGet("literal.test", "/", nil); r.Header.Get("Location") != "https://static.example/a?x=1&y=2" {
		t.Fatalf("literal: %q", r.Header.Get("Location"))
	}
	// The redirect is given only after authentication, upgrades included.
	e.client.Cnf.U, e.client.Cnf.P = "u", "p"
	if r := e.mustGet("redir.test", "/p", nil); r.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", r.StatusCode)
	}
	if _, _, resp := e.wsDial("redir.test", "/ws", nil, false); resp.StatusCode != 401 {
		t.Fatalf("unauthenticated upgrade: %d", resp.StatusCode)
	}
	if _, _, resp := e.wsDial("redir.test", "/ws", basicAuth("u", "p"), false); resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("authenticated upgrade: %d", resp.StatusCode)
	}
	if r := e.mustGet("redir.test", "/p", basicAuth("u", "p")); r.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("authenticated: %d", r.StatusCode)
	}
	if len(e.bridge.opened()) != 0 {
		t.Fatal("link opened for a redirect")
	}
}

func TestDiffRewrites(t *testing.T) {
	withAddOriginHeader(t)
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	rules := "X-Test: ${request_host}|${request_path}\nConnection: hijacked"
	e.addHost("rw.test", b.addr, func(h *model.Host) {
		h.HostChange = "inner.example"
		h.HeaderChange = rules
		h.RespHeaderChange = "X-Resp: ${status_code}\nX-Backend: ${unset}"
		h.Location = "/api"
		h.PathRewrite = "/v2"
	})
	e.addHost("rwcompat.test", b.addr, func(h *model.Host) {
		h.HeaderChange = rules
		h.Location = "/api"
		h.PathRewrite = "/v2"
		h.CompatMode = true
	})

	r := e.mustGet("rw.test", "/api/x", http.Header{"Origin": {"http://rw.test"}})
	m := r.info(t)
	want := map[string]string{
		"host": "inner.example", "path": "/v2/x", "xop": "/api/x", "origin": "http://inner.example",
		"xtest": "rw.test|/v2/x", "xfh": "", "xfp": "http", "xrip": "127.0.0.1", "conn": "",
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("%s = %q, want %q (%v)", k, m[k], v, m)
		}
	}
	if r.Header.Get("X-Resp") != "200" || r.Header.Get("X-Backend") != "" {
		t.Fatalf("response headers %v", r.Header)
	}
	// Over HTTPS the scheme follows the client connection.
	r, err := e.getTLS("rw.test", "/api/y", http.Header{"Origin": {"https://rw.test"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if m := r.info(t); m["origin"] != "https://inner.example" || m["xfp"] != "https" || m["path"] != "/v2/y" {
		t.Fatalf("HTTPS: %v", m)
	}
	// A path outside the location is left alone; compatibility mode drops
	// X-Original-Path.
	m = e.mustGet("rwcompat.test", "/api/z", nil).info(t)
	if m["path"] != "/v2/z" || m["xop"] != "" || m["xfh"] != "rwcompat.test" || m["xfp"] != "http" {
		t.Fatalf("compat: %v", m)
	}

	// WebSocket upgrades get the same host and header rules and path rewrite.
	e.addHost("rwws.test", b.addr, func(h *model.Host) {
		h.HostChange = "inner.example"
		h.HeaderChange = "X-Test: ws-${request_path}"
		h.Location = "/sock"
		h.PathRewrite = "/ws"
	})
	c, br, resp := e.wsDial("rwws.test", "/sock", nil, false)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-host") != "inner.example" || resp.Header.Get("X-Got-path") != "/ws" ||
		resp.Header.Get("X-Got-xtest") != "ws-/ws" || resp.Header.Get("X-Got-xfp") != "http" || resp.Header.Get("X-Got-xrip") != "127.0.0.1" {
		t.Fatalf("WebSocket: %d %v", resp.StatusCode, resp.Header)
	}
	if resp.Header.Get("X-Got-xff") != "" {
		t.Fatalf("WebSocket got X-Forwarded-For %q", resp.Header.Get("X-Got-xff"))
	}
	echoes(t, c, br)
}

func TestDiffAutoCORS(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("cors.test", b.addr, func(h *model.Host) { h.AutoCORS = true })
	e.addHost("nocors.test", b.addr, nil)

	r := e.mustGet("cors.test", "/", http.Header{"Origin": {"http://o.example"}})
	if r.Header.Get("Access-Control-Allow-Origin") != "http://o.example" || r.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("CORS headers %v", r.Header)
	}
	if r := e.mustGet("cors.test", "/", nil); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header without Origin")
	}
	if r := e.mustGet("cors.test", "/cors-own", http.Header{"Origin": {"http://o.example"}}); r.Header.Get("Access-Control-Allow-Origin") != "own" || r.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("backend CORS overridden: %v", r.Header)
	}
	pre, err := e.do(http.MethodOptions, "cors.test", "/", http.Header{"Origin": {"http://o.example"}, "Access-Control-Request-Method": {"GET"}})
	if err != nil {
		t.Fatal(err)
	} else if pre.StatusCode != http.StatusNoContent || pre.Header.Get("Access-Control-Allow-Origin") != "http://o.example" || pre.Header.Get("Access-Control-Allow-Methods") != "GET" {
		t.Fatalf("preflight: %v %v", pre.Response, err)
	}
	if r := e.mustGet("nocors.test", "/", http.Header{"Origin": {"http://o.example"}}); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header without AutoCORS")
	}
}

func TestDiffTargetIsHttps(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "tls", true)
	e.addHost("tls.test", b.addr, func(h *model.Host) { h.TargetIsHttps = true })
	e.addHost("tlshc.test", b.addr, func(h *model.Host) { h.TargetIsHttps = true; h.HostChange = "inner.example:8443" })

	m := e.mustGet("tls.test", "/x", nil).info(t)
	if m["tls"] != "true" || m["sni"] != "tls.test" || m["host"] != "tls.test" {
		t.Fatalf("backend saw %v", m)
	}
	m = e.mustGet("tlshc.test", "/x", nil).info(t)
	if m["sni"] != "inner.example" || m["host"] != "inner.example:8443" {
		t.Fatalf("with host change: %v", m)
	}
	c, br, resp := e.wsDial("tls.test", "/ws", nil, true)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-tls") != "true" || resp.Header.Get("X-Got-sni") != "tls.test" {
		t.Fatalf("WebSocket: %d %v", resp.StatusCode, resp.Header)
	}
	echoes(t, c, br)
	// A plain backend fails the TLS handshake.
	plain := newBackend(t, "plain", false)
	e.addHost("tlsbad.test", plain.addr, func(h *model.Host) { h.TargetIsHttps = true })
	if r := e.mustGet("tlsbad.test", "/", nil); r.StatusCode != http.StatusBadGateway || r.body != "nps 404" {
		t.Fatalf("plain backend: %d %q", r.StatusCode, r.body)
	}
	if _, _, resp := e.wsDial("tlsbad.test", "/ws", nil, false); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("plain backend upgrade: %d", resp.StatusCode)
	}
}

func TestDiffCompatModeWebSocketHeaders(t *testing.T) {
	withAddOriginHeader(t)
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	e.addHost("compat.test", b.addr, func(h *model.Host) { h.CompatMode = true; h.HeaderChange = "X-Test: set" })
	c, br, resp := e.wsDial("compat.test", "/ws", nil, false)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-xfp") != "" || resp.Header.Get("X-Got-xrip") != "" || resp.Header.Get("X-Got-xtest") != "set" {
		t.Fatalf("WebSocket: %d %v", resp.StatusCode, resp.Header)
	}
	echoes(t, c, br)
	// Regular requests keep the forwarding headers in compatibility mode.
	if m := e.mustGet("compat.test", "/", nil).info(t); m["xfp"] != "http" || m["xrip"] != "127.0.0.1" || m["xff"] != "127.0.0.1" {
		t.Fatalf("regular request: %v", m)
	}
}

func TestDiffLocalProxy(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run("allow="+strconv.FormatBool(allow), func(t *testing.T) {
			e := newProxyEnv(t, envOptions{localProxy: allow})
			b := newBackend(t, "a", false)
			e.addHost("local.test", b.addr, func(h *model.Host) { h.Target.LocalProxy = true })
			if r := e.mustGet("local.test", "/", nil); r.StatusCode != 200 {
				t.Fatalf("status %d", r.StatusCode)
			}
			c, br, resp := e.wsDial("local.test", "/ws", nil, false)
			if resp.StatusCode != 101 {
				t.Fatalf("WebSocket %d", resp.StatusCode)
			}
			echoes(t, c, br)
			for _, l := range e.bridge.opened() {
				if l.LocalProxy != allow {
					t.Fatalf("link LocalProxy = %v, want %v", l.LocalProxy, allow)
				}
			}
		})
	}
}

func TestDiffTargetRotation(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	a, b := newBackend(t, "a", false), newBackend(t, "b", false)
	e.addHost("rot.test", a.addr+"\n"+b.addr, func(h *model.Host) { h.CompatMode = true })
	seen := map[string]int{}
	for range 4 {
		seen[e.mustGet("rot.test", "/", nil).Header.Get("X-Backend")]++
	}
	if seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("requests per backend %v", seen)
	}
	seen = map[string]int{}
	for range 2 {
		c, br, resp := e.wsDial("rot.test", "/ws", nil, false)
		seen[resp.Header.Get("X-Got-backend")]++
		echoes(t, c, br)
		_ = c.Close()
	}
	if seen["a"] != 1 || seen["b"] != 1 {
		t.Fatalf("upgrades per backend %v", seen)
	}
}

func TestDiffUpstreamFailures(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	e.addHost("dead.test", deadAddr, nil)
	e.addHost("none.test", "", nil)

	for _, name := range []string{"dead.test", "none.test"} {
		if r := e.mustGet(name, "/", nil); r.StatusCode != http.StatusBadGateway || r.body != "nps 404" {
			t.Fatalf("%s: %d %q", name, r.StatusCode, r.body)
		}
		if _, _, resp := e.wsDial(name, "/ws", nil, false); resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("%s upgrade: %d", name, resp.StatusCode)
		}
	}

	// A backend that closes without answering.
	hang, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hangWG sync.WaitGroup
	hangWG.Go(func() {
		for {
			c, err := hang.Accept()
			if err != nil {
				return
			}
			_, _ = c.Read(make([]byte, 1024))
			_ = c.Close()
		}
	})
	defer func() { _ = hang.Close(); hangWG.Wait() }()
	e.addHost("eof.test", hang.Addr().String(), nil)
	// The backend's EOF is told by status 521.
	if r := e.mustGet("eof.test", "/", nil); r.StatusCode != 521 || r.body != "" {
		t.Fatalf("eof: %d %q", r.StatusCode, r.body)
	}
	if _, _, resp := e.wsDial("eof.test", "/ws", nil, false); resp.StatusCode != http.StatusBadGateway || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("eof upgrade: %d %v", resp.StatusCode, resp.Header)
	}

	// Errors of the client's limits are told apart.
	b := newBackend(t, "a", false)
	e.addHost("limit.test", b.addr, nil)
	flowMsg := "Client 1 flow limit exceeded"
	//noinspection GoErrorStringFormat
	e.bridge.setFail(errors.New(flowMsg))
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 429 || r.body != "Client 1 flow limit exceeded\n" {
		t.Fatalf("limit: %d %q", r.StatusCode, r.body)
	}
	e.hp.FlowLimitErrorContent = []byte("flow page")
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 429 || r.body != "flow page" {
		t.Fatalf("limit page: %d %q", r.StatusCode, r.body)
	}
	timeMsg := "Task 1 time limit exceeded"
	//noinspection GoErrorStringFormat
	e.bridge.setFail(errors.New(timeMsg))
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 429 || r.body != "Task 1 time limit exceeded\n" {
		t.Fatalf("time limit: %d %q", r.StatusCode, r.body)
	}
	e.bridge.setFail(nil)
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 200 {
		t.Fatalf("after failures: %d", r.StatusCode)
	}
}

func TestDiffUpgradeRefusalAndUnauthenticatedUpgrade(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	h := e.addHost("ws.test", b.addr, func(h *model.Host) { h.RespHeaderChange = "X-Resp: ${status_code}" })

	c, br, resp := e.wsDial("ws.test", "/ws", nil, false)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-host") != "ws.test" {
		t.Fatalf("handshake %d %v", resp.StatusCode, resp.Header)
	}
	echoes(t, c, br)
	_ = c.Close()
	waitFlows(t, h.Flow, e.client.Flow)

	_, br, resp = e.wsDial("ws.test", "/ws-deny", nil, false)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 || string(body) != "denied" || !resp.Close || resp.Header.Get("X-Resp") != "403" || resp.Header.Get("X-Backend") != "a" {
		t.Fatalf("refusal %d %q %v", resp.StatusCode, body, resp.Header)
	}
	_ = br

	// Upgrades go on without credentials, but only a completed handshake
	// reaches the client.
	e.client.Cnf.U, e.client.Cnf.P = "u", "p"
	c, br, resp = e.wsDial("ws.test", "/ws", nil, false)
	if resp.StatusCode != 101 {
		t.Fatalf("unauthenticated upgrade %d", resp.StatusCode)
	}
	echoes(t, c, br)
	_, _, resp = e.wsDial("ws.test", "/ws-deny", nil, false)
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != `Basic realm="Restricted"` {
		t.Fatalf("unauthenticated refusal %d %v", resp.StatusCode, resp.Header)
	}

	// Over HTTPS.
	c, br, resp = e.wsDial("ws.test", "/ws", basicAuth("u", "p"), true)
	if resp.StatusCode != 101 || resp.Header.Get("X-Got-tls") != "false" {
		t.Fatalf("HTTPS upgrade %d %v", resp.StatusCode, resp.Header)
	}
	echoes(t, c, br)
}

func TestDiffProxyProtocol(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	headers := make(chan string, 8)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				br := bufio.NewReader(c)
				line, _ := br.ReadString('\n')
				headers <- line
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Header.Get("Upgrade") != "" {
					_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nhello")
					_, _ = io.Copy(c, br)
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				_, _ = io.Copy(io.Discard, br)
			})
		}
	})
	defer func() { _ = l.Close(); wg.Wait() }()
	e.addHost("pp.test", l.Addr().String(), func(h *model.Host) { h.Target.ProxyProtocol = 1 })
	for range 2 {
		if r := e.mustGet("pp.test", "/", nil); r.StatusCode != 200 || r.body != "ok" {
			t.Fatalf("status %d %q", r.StatusCode, r.body)
		}
	}
	c, br, resp := e.wsDial("pp.test", "/ws", nil, false)
	if resp.StatusCode != 101 {
		t.Fatalf("upgrade %d", resp.StatusCode)
	}
	echoes(t, c, br)
	_ = c.Close()
	for range 3 {
		line := <-headers
		if !strings.HasPrefix(line, "PROXY TCP4 127.0.0.1 127.0.0.1 ") {
			t.Fatalf("PROXY header %q", line)
		}
	}
	// Each request had a backend connection of its own.
	if n := len(e.bridge.opened()); n != 3 {
		t.Fatalf("%d links, want 3", n)
	}
}

func TestDiffLimits(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	h := e.addHost("limit.test", b.addr, nil)
	h.SetMaxConn(1)

	slow := make(chan reply, 1)
	go func() {
		r, _ := e.get("limit.test", "/slow", nil)
		slow <- r
	}()
	<-b.entered
	r := e.mustGet("limit.test", "/", nil)
	if r.StatusCode != 429 || !strings.HasPrefix(r.body, "Access denied: ") {
		t.Fatalf("over the connection limit: %d %q", r.StatusCode, r.body)
	}
	_, _, resp := e.wsDial("limit.test", "/ws", nil, false)
	if resp.StatusCode != 429 {
		t.Fatalf("upgrade over the connection limit: %d", resp.StatusCode)
	}
	close(b.release)
	if r := <-slow; r.Response == nil || r.StatusCode != 200 {
		t.Fatalf("slow request: %v", r.Response)
	}
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 200 {
		t.Fatalf("after release: %d", r.StatusCode)
	}

	// An expired client is refused before anything is dialed.
	links := len(e.bridge.opened())
	e.client.Flow.SetLimits(0, time.Now().Add(-time.Hour))
	if r := e.mustGet("limit.test", "/", nil); r.StatusCode != 429 || r.body != "Access denied: service access expired\n" {
		t.Fatalf("expired: %d %q", r.StatusCode, r.body)
	}
	if len(e.bridge.opened()) != links {
		t.Fatal("link opened for an expired client")
	}
	e.client.Flow.SetLimits(0, time.Time{})

	// Rate limits slow the response down.
	e.addHost("rate.test", b.addr, func(h *model.Host) { h.UploadLimit, h.DownloadLimit = 32, 32 })
	start := time.Now()
	if r := e.mustGet("rate.test", "/big", nil); r.StatusCode != 200 || len(r.body) != 96<<10 {
		t.Fatalf("rate limited: %d %d", r.StatusCode, len(r.body))
	}
	if d := time.Since(start); d < time.Second {
		t.Fatalf("96 KiB at 32 KiB/s took %v", d)
	}
}

func TestDiffHTTP3AndAltSvc(t *testing.T) {
	e := newProxyEnv(t, envOptions{http3: true})
	b := newBackend(t, "a", false)
	e.addHost("h3.test", b.addr, nil)
	e.addHost("h3compat.test", b.addr, func(h *model.Host) { h.CompatMode = true })
	want := `h3=":` + e.hp.Http3PortStr + `"; ma=86400`

	r, err := e.getTLS("h3.test", "/", nil, false)
	if err != nil {
		t.Fatal(err)
	} else if r.Header.Get("Alt-Svc") != want {
		t.Fatalf("HTTPS Alt-Svc %q, %v", r.Header.Get("Alt-Svc"), err)
	}
	if r := e.mustGet("h3.test", "/", nil); r.Header.Get("Alt-Svc") != "" {
		t.Fatal("Alt-Svc over plain HTTP")
	}
	if r, err := e.getTLS("h3compat.test", "/", nil, false); err != nil {
		t.Fatal(err)
	} else if r.Header.Get("Alt-Svc") != "" {
		t.Fatalf("compat Alt-Svc %q, %v", r.Header.Get("Alt-Svc"), err)
	}
	r, err = e.getH3("h3.test", "/q")
	if err != nil {
		t.Fatal(err)
	}
	if r.ProtoMajor != 3 || r.StatusCode != 200 || r.Header.Get("Alt-Svc") != want {
		t.Fatalf("HTTP/3: %d %d %v", r.ProtoMajor, r.StatusCode, r.Header)
	}
	if m := r.info(t); m["path"] != "/q" || m["host"] != "h3.test" {
		t.Fatalf("HTTP/3 backend saw %v", m)
	}
}

// An edited host is served with its new settings from the next request on.
func TestDiffHostEditAppliesToNextRequest(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	a, b := newBackend(t, "a", false), newBackend(t, "b", false)
	h := e.addHost("edit.test", a.addr, nil)
	if r := e.mustGet("edit.test", "/", nil); r.Header.Get("X-Backend") != "a" || r.info(t)["xtest"] != "" {
		t.Fatalf("before: %v", r.Header)
	}
	e.editHost(h, func(h *model.Host) {
		h.HeaderChange = "X-Test: edited"
		h.Target = &model.Target{TargetStr: b.addr}
	})
	if r := e.mustGet("edit.test", "/", nil); r.Header.Get("X-Backend") != "b" || r.info(t)["xtest"] != "edited" {
		t.Fatalf("after edit: %v %q", r.Header, r.body)
	}
	e.editHost(h, func(h *model.Host) { h.AutoHttps = true })
	if r := e.mustGet("edit.test", "/", nil); r.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("after enabling AutoHttps: %d", r.StatusCode)
	}
	e.editHost(h, func(h *model.Host) { h.AutoHttps = false; h.RedirectURL = "https://moved.example/" })
	if r := e.mustGet("edit.test", "/", nil); r.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("after setting a redirect: %d", r.StatusCode)
	}

}
