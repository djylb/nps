package httpproxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
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

// By default an unknown host gets no response at all: a probe sees the
// connection close, not a site.
func TestFallbackUnknownHostDefaultClose(t *testing.T) {
	hs, store := fallbackEnv(t, false)
	a, b := net.Pipe()
	defer a.Close()
	if !hs.serveFallback(b, nil) {
		t.Fatal("refused")
	}
	resp, err := exchange(t, a)
	if err == nil {
		t.Fatalf("got %v", resp.Status)
	}
	if !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	<-store.seen
}
