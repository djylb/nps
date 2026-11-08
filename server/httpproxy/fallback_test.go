package httpproxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
)

// tlsSeenStore answers no host and records the TLS of the request.
type tlsSeenStore struct {
	Store
	seen chan *tls.ConnectionState
}

func (s *tlsSeenStore) GetInfoByHost(_ string, r *http.Request) (*model.Host, error) {
	s.seen <- r.TLS
	return nil, errors.New("no host")
}

func fallbackEnv(t *testing.T, always bool) (*HttpServer, *tlsSeenStore) {
	t.Helper()
	store := &tlsSeenStore{seen: make(chan *tls.ConnectionState, 1)}
	hp := &HttpProxy{BaseServer: proxy.NewBaseServer(nil, nil, false), store: store}
	hp.ErrorContent = []byte("page")
	hp.ErrorAlways = always
	hs := NewHttpServer(hp, nil)
	hp.HttpServer = hs
	t.Cleanup(func() { hs.closeFallback() })
	return hs, store
}

func exchange(t *testing.T, c net.Conn) (*http.Response, error) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: unknown.example\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	return http.ReadResponse(bufio.NewReader(c), nil)
}

func TestFallbackPlainConn(t *testing.T) {
	hs, store := fallbackEnv(t, true)
	a, b := net.Pipe()
	defer a.Close()
	if !hs.serveFallback(b, nil) {
		t.Fatal("refused")
	}
	resp, err := exchange(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
	if st := <-store.seen; st != nil {
		t.Fatal("plain request has TLS")
	}
}

// The case that matters: a connection whose TLS ended in the bridge, with its
// bytes replayed through a wrapper that is no longer a *tls.Conn.
func TestFallbackAfterTLSTermination(t *testing.T) {
	hs, store := fallbackEnv(t, true)
	tc0 := newTestCert(t, "bridge.example")
	cert, err := tls.X509KeyPair(tc0.certPEM, tc0.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cl, sv := net.Pipe()
	defer cl.Close()
	tc := tls.Server(sv, &tls.Config{Certificates: []tls.Certificate{cert}})
	client := tls.Client(cl, &tls.Config{InsecureSkipVerify: true})
	done := make(chan error, 1)
	go func() { done <- tc.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := tc.ConnectionState()
	if !hs.serveFallback(netx.NewPrefixConn(tc, nil), &st) {
		t.Fatal("refused")
	}
	resp, err := exchange(t, client)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
	if seen := <-store.seen; seen == nil || !seen.HandshakeComplete {
		t.Fatal("the request does not see its TLS")
	}
}

// An unknown host on the fallback path gets a plain 404, never a closed
// connection, whatever error_always says; the page is not the error page, and
// the built-in one names no software.
func TestFallbackUnknownHostIs404(t *testing.T) {
	for _, always := range []bool{false, true} {
		hs, store := fallbackEnv(t, always)
		a, b := net.Pipe()
		if !hs.serveFallback(b, nil) {
			t.Fatal("refused")
		}
		resp, err := exchange(t, a)
		if err != nil {
			t.Fatalf("error_always=%v: %v", always, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = a.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("error_always=%v: %s", always, resp.Status)
		}
		if strings.Contains(strings.ToLower(string(body)+resp.Header.Get("Server")), "nps") || strings.Contains(string(body), "page") {
			t.Fatalf("body %q names the software or is the error page", body)
		}
		if len(body) == 0 || resp.Header.Get("Server") != "" {
			t.Fatalf("body %q, Server %q", body, resp.Header.Get("Server"))
		}
		<-store.seen
	}
}

func TestFallbackCustom404Body(t *testing.T) {
	hs, store := fallbackEnv(t, false)
	path := filepath.Join(t.TempDir(), "404.html")
	if err := os.WriteFile(path, []byte("gone fishing"), 0o600); err != nil {
		t.Fatal(err)
	}
	hs.Fallback404Content = LoadFallback404(path)
	a, b := net.Pipe()
	defer a.Close()
	if !hs.serveFallback(b, nil) {
		t.Fatal("refused")
	}
	resp, err := exchange(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusNotFound || string(body) != "gone fishing" {
		t.Fatalf("%s %q", resp.Status, body)
	}
	<-store.seen
	// an unreadable file falls back to the built-in body
	if got := LoadFallback404(filepath.Join(t.TempDir(), "missing")); string(got) != defaultNotFound {
		t.Fatalf("%q", got)
	}
}

// A node without a domain proxy answers the same way.
func TestNotFoundWithoutDomainProxy(t *testing.T) {
	n := NewNotFound([]byte(defaultNotFound))
	defer n.Close()
	a, b := net.Pipe()
	defer a.Close()
	if !n.ServeFallback(b, nil) {
		t.Fatal("refused")
	}
	resp, err := exchange(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
	_ = n.Close()
	if n.ServeFallback(nil, nil) {
		t.Fatal("closed NotFound took a connection")
	}
}

// A known domain is handled as that route: the matching host's certificate is
// chosen by the same rules as the domain proxy's, and a name no route has
// gets none, leaving the caller's default.
func TestFallbackCertBySNI(t *testing.T) {
	e := newCertSelectEnv(t)
	own := newTestCert(t, "own.example.com")
	e.addHost(1, 1, "own.example.com", "/", &own)
	e.addHost(2, 1, "default.test", "/", nil)
	e.addHost(3, 2, "bare.example.com", "/", nil)
	hello := func(sni string) *tls.ClientHelloInfo { return &tls.ClientHelloInfo{ServerName: sni} }
	if c := e.s.fallbackCert(hello("own.example.com")); c == nil || !bytes.Equal(c.Certificate[0], own.der) {
		t.Fatal("the route's own certificate is not chosen")
	}
	// no certificate of its own: the default one, which covers this name
	if c := e.s.fallbackCert(hello("default.test")); c == nil || !bytes.Equal(c.Certificate[0], e.deflt.der) {
		t.Fatal("the default certificate is not chosen for a route it covers")
	}
	// a route nothing covers, a name without a route, no name
	for _, sni := range []string{"bare.example.com", "unknown.example.com", ""} {
		if c := e.s.fallbackCert(hello(sni)); c != nil {
			t.Fatalf("%q got a certificate", sni)
		}
	}
	// the default certificate never goes to a name no route has
	if c := e.s.fallbackCert(hello("default.test.unrouted")); c != nil {
		t.Fatal("default certificate leaked to an unrouted name")
	}
	// a proxy with no HTTPS server offers nothing
	hp := &HttpProxy{BaseServer: proxy.NewBaseServer(nil, nil, false)}
	if c, err := hp.FallbackCert(hello("own.example.com")); c != nil || err != nil {
		t.Fatal(c, err)
	}
}
