package httpproxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
)

func TestServeReturnsListenerError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	withNetwork(t, func(n *servercfg.NetworkConfig) {
		n.HTTPProxyIP, n.HTTPProxyPort = "127.0.0.1", taken.Addr().(*net.TCPAddr).Port
	})

	s := &HttpProxy{BaseServer: proxy.NewBaseServer(nil, nil, false), HttpPort: connection.Network().HTTPProxyPort}
	err = s.serve()
	if err == nil || !strings.Contains(err.Error(), "HTTP listener") {
		t.Fatalf("serve() = %v, want an HTTP listener error", err)
	}
}

// When HTTPS cannot bind, serve reports it with HTTP already running, and the
// Close that its caller then runs frees HTTP.
func TestServeReportsPartialBindAndCloseStopsTheRest(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	withNetwork(t, func(n *servercfg.NetworkConfig) {
		n.HTTPProxyIP, n.HTTPProxyPort = "127.0.0.1", free
		n.HTTPSProxyPort = taken.Addr().(*net.TCPAddr).Port
	})

	s := &HttpProxy{
		BaseServer:     proxy.NewBaseServer(nil, nil, false),
		HttpPort:       free,
		HttpsPort:      connection.Network().HTTPSProxyPort,
		HttpProxyCache: index.NewAnyIntIndex(),
	}
	if err := s.serve(); err == nil || !strings.Contains(err.Error(), "HTTPS listener") {
		t.Fatalf("serve() = %v", err)
	}
	_ = s.Close()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(free)))
	if err != nil {
		t.Fatalf("HTTP port not freed: %v", err)
	}
	_ = l.Close()
}

func TestStopErrorHidesClosing(t *testing.T) {
	for _, err := range []error{nil, http.ErrServerClosed, net.ErrClosed} {
		if got := stopError("HTTP", err); got != nil {
			t.Fatalf("stopError(%v) = %v", err, got)
		}
	}
	if stopError("HTTP", io.ErrUnexpectedEOF) == nil {
		t.Fatal("stopError hid a failure")
	}
}

// A connection of a host with a PROXY header must not serve a second visitor.
// Close may run at any point of serve: serve must end either way, and a
// server it bound must not outlive Close.
func TestServeAndCloseConcurrently(t *testing.T) {
	for range 20 {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		withNetwork(t, func(n *servercfg.NetworkConfig) {
			n.HTTPProxyIP, n.HTTPProxyPort = "127.0.0.1", probe.Addr().(*net.TCPAddr).Port
		})
		_ = probe.Close()

		s := &HttpProxy{
			BaseServer:     proxy.NewBaseServer(nil, nil, false),
			HttpPort:       connection.Network().HTTPProxyPort,
			HttpProxyCache: index.NewAnyIntIndex(),
		}
		done := make(chan error, 1)
		go func() { done <- s.serve() }()
		_ = s.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, proxy.ErrServerClosed) {
				t.Fatalf("serve() = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serve() did not end after Close()")
		}
	}
}

func TestProxyProtocolConnectionsAreNotReused(t *testing.T) {
	var accepted atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	backend.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			accepted.Add(1)
		}
	}
	backend.Start()
	defer backend.Close()

	count := func(proxyProtocol int) int32 {
		accepted.Store(0)
		s := &HttpServer{HttpProxy: &HttpProxy{}}
		// The route decides, not the target of the last publisher on the host.
		tr := s.newTransports(&file.Host{Target: &file.Target{}}).forRoute(file.Route{Target: &file.Target{ProxyProtocol: proxyProtocol}})
		tr.DialContext, tr.DialTLSContext = nil, nil
		defer tr.CloseIdleConnections()
		for range 2 {
			req, _ := http.NewRequest(http.MethodGet, backend.URL, nil)
			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
		return accepted.Load()
	}
	if n := count(1); n != 2 {
		t.Fatalf("with PROXY header: %d backend connections, want 2", n)
	}
	if n := count(0); n != 1 {
		t.Fatalf("without PROXY header: %d backend connections, want 1", n)
	}
}

// withNetwork changes the listener settings for the rest of the test.
func withNetwork(t *testing.T, change func(*servercfg.NetworkConfig)) {
	t.Helper()
	old := connection.Network()
	t.Cleanup(func() { connection.Configure(old) })
	n := old
	change(&n)
	connection.Configure(n)
}
