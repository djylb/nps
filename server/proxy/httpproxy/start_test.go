package httpproxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server/connection"
)

func TestServeReturnsListenerError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	ip, port := connection.HttpIp, connection.HttpPort
	defer func() { connection.HttpIp, connection.HttpPort = ip, port }()
	connection.HttpIp = "127.0.0.1"
	connection.HttpPort = taken.Addr().(*net.TCPAddr).Port

	s := &HttpProxy{HttpPort: connection.HttpPort}
	err = s.serve()
	if err == nil || !strings.Contains(err.Error(), "HTTP listener") {
		t.Fatalf("serve() = %v, want an HTTP listener error", err)
	}
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
		tr := s.newTransport(&file.Host{Target: &file.Target{ProxyProtocol: proxyProtocol}})
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
