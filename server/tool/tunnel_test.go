package tool

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/djylb/nps/lib/conn"
)

type stubDialer struct {
	dialFn func(remote string) (net.Conn, error)
}

func (s *stubDialer) DialVirtual(remote string) (net.Conn, error) {
	if s.dialFn == nil {
		return nil, nil
	}
	return s.dialFn(remote)
}

func (s *stubDialer) ServeVirtual(c net.Conn) {}

func setLookupForTest(t *testing.T, fn func(int) (Dialer, bool)) {
	t.Helper()
	old := lookup.Load()
	lookup.Store(fn)
	t.Cleanup(func() {
		lookup = atomic.Value{}
		if old != nil {
			lookup.Store(old.(func(int) (Dialer, bool)))
		}
	})
}

func TestGetTunnelConnWhenLookupNotSet(t *testing.T) {
	lookup = atomic.Value{}

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel lookup not set") {
		t.Fatalf("GetTunnelConn() err=%v, want tunnel lookup not set", err)
	}
}

func TestGetTunnelConnWhenTunnelNotFound(t *testing.T) {
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return nil, false
	})

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel not found") {
		t.Fatalf("GetTunnelConn() err=%v, want tunnel not found", err)
	}
}

func TestGetTunnelConnDelegatesToDialer(t *testing.T) {
	called := false
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return &stubDialer{dialFn: func(remote string) (net.Conn, error) {
			called = true
			if remote != "127.0.0.1:8080" {
				t.Fatalf("DialVirtual remote=%q, want 127.0.0.1:8080", remote)
			}
			a, b := net.Pipe()
			_ = b.Close()
			return a, nil
		}}, true
	})

	c, err := GetTunnelConn(7, "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("GetTunnelConn() err=%v, want nil", err)
	}
	if !called {
		t.Fatal("GetTunnelConn() did not call dialer")
	}
	_ = c.Close()
}

func TestGetTunnelConnPropagatesDialError(t *testing.T) {
	wantErr := errors.New("dial failed")
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return &stubDialer{dialFn: func(remote string) (net.Conn, error) {
			return nil, wantErr
		}}, true
	})

	_, err := GetTunnelConn(7, "127.0.0.1:8080")
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetTunnelConn() err=%v, want %v", err, wantErr)
	}
}

func TestGetWebServerConnWhenListenerNotSet(t *testing.T) {
	orig := WebServerListener
	WebServerListener = nil
	t.Cleanup(func() { WebServerListener = orig })

	_, err := GetWebServerConn("127.0.0.1:8080")
	if err == nil || !strings.Contains(err.Error(), "web server not set") {
		t.Fatalf("GetWebServerConn() err=%v, want web server not set", err)
	}
}

func TestGetWebServerConnDialSuccess(t *testing.T) {
	orig := WebServerListener
	l := conn.NewVirtualListener(conn.LocalTCPAddr)
	WebServerListener = l
	t.Cleanup(func() {
		_ = l.Close()
		WebServerListener = orig
	})

	c, err := GetWebServerConn("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("GetWebServerConn() err=%v, want nil", err)
	}
	defer c.Close()

	accepted, err := l.Accept()
	if err != nil {
		t.Fatalf("VirtualListener.Accept() err=%v, want nil", err)
	}
	defer accepted.Close()

	payload := []byte("ok")
	go func() {
		_, _ = accepted.Write(payload)
	}()

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("ReadFull() err=%v, want nil", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("received %q, want %q", string(buf), string(payload))
	}
}
