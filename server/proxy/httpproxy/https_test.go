package httpproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/server/proxy"
)

func TestHttpsListenerPushAfterCloseDoesNotPanic(t *testing.T) {
	l := NewHttpsListener(nil)
	_ = l.Close()

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		l.push(tls.Server(server, &tls.Config{}))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a closed listener")
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("pushed connection was not closed")
	}

	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() error = %v, want net.ErrClosed", err)
	}
}

// startHTTPS serves e's server on a loopback HTTPS listener, answering
// requests for unknown hosts with the error page.
func startHTTPS(t *testing.T, e *certSelectEnv) string {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &trackingListener{Listener: inner}
	s := e.s.HttpsServer
	s.HttpProxy.BaseServer = &proxy.BaseServer{ErrorContent: []byte("nps 404")}
	s.ErrorAlways = true
	s.tlsNextProtos = []string{"http/1.1"}
	s.httpsListener = l
	s.httpsServeListener = NewHttpsListener(l)
	s.httpsServer = s.NewServer(0, "https")
	served := make(chan struct{})
	started := make(chan struct{})
	go func() { defer close(served); _ = s.httpsServer.Serve(s.httpsServeListener) }()
	go func() { defer close(started); _ = s.Start() }()
	t.Cleanup(func() {
		_ = l.Close()
		_ = s.httpsServer.Close()
		<-served
		<-started
		l.conns.Wait()
	})
	return l.Addr().String()
}

// trackingListener counts the connections it accepted until they close, so
// a test can wait for every connection handler to finish.
type trackingListener struct {
	net.Listener
	conns sync.WaitGroup
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.conns.Add(1)
	return &trackedConn{Conn: c, done: l.conns.Done}, nil
}

type trackedConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.done)
	return err
}

func httpsGet(addr, sni string, cert testCert) (*http.Response, []byte, error) {
	pool := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(cert.der)
	if err != nil {
		return nil, nil, err
	}
	pool.AddCert(leaf)
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{ServerName: sni, RootCAs: pool},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Host = sni
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp, body, err
}

func TestHttpsNoEntryServesErrorPageWithDefaultCert(t *testing.T) {
	e := newCertSelectEnv(t)
	addr := startHTTPS(t, e)
	resp, body, err := httpsGet(addr, "default.test", e.deflt)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound || string(body) != "nps 404" {
		t.Fatalf("got %d %q, want the error page", resp.StatusCode, body)
	}
}

func TestHttpsNoEntryWithoutDefaultCertCloses(t *testing.T) {
	e := newCertSelectEnv(t)
	e.s.hasDefaultCert = false
	addr := startHTTPS(t, e)
	if _, _, err := httpsGet(addr, "default.test", e.deflt); err == nil {
		t.Fatal("handshake succeeded without an entry or a default certificate")
	}
}

func TestHttpsNoEntryAbortsWithoutErrorPage(t *testing.T) {
	e := newCertSelectEnv(t)
	addr := startHTTPS(t, e)
	e.s.ErrorAlways = false
	if resp, _, err := httpsGet(addr, "default.test", e.deflt); err == nil {
		t.Fatalf("got %d, want the request aborted", resp.StatusCode)
	}
}
