package bridge

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
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

func (s *stubDialer) ServeVirtual(_ net.Conn) {}

func bridgeWithLookup(fn TunnelLookup) *Bridge {
	b := newBridge(nil, 30)
	b.SetTunnelLookup(fn)
	return b
}

func TestGetTunnelConnWhenLookupNotSet(t *testing.T) {
	_, err := newBridge(nil, 30).TunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel lookup not set") {
		t.Fatalf("TunnelConn() err=%v, want tunnel lookup not set", err)
	}
}

func TestGetTunnelConnWhenTunnelNotFound(t *testing.T) {
	b := bridgeWithLookup(func(id int) (TunnelDialer, bool) {
		return nil, false
	})

	_, err := b.TunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel not found") {
		t.Fatalf("TunnelConn() err=%v, want tunnel not found", err)
	}
}

func TestGetTunnelConnDelegatesToDialer(t *testing.T) {
	called := false
	b := bridgeWithLookup(func(id int) (TunnelDialer, bool) {
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

	c, err := b.TunnelConn(7, "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("TunnelConn() err=%v, want nil", err)
	}
	if !called {
		t.Fatal("b.TunnelConn() did not call dialer")
	}
	_ = c.Close()
}

func TestGetTunnelConnPropagatesDialError(t *testing.T) {
	wantErr := errors.New("dial failed")
	b := bridgeWithLookup(func(id int) (TunnelDialer, bool) {
		return &stubDialer{dialFn: func(remote string) (net.Conn, error) {
			return nil, wantErr
		}}, true
	})

	_, err := b.TunnelConn(7, "127.0.0.1:8080")
	if !errors.Is(err, wantErr) {
		t.Fatalf("TunnelConn() err=%v, want %v", err, wantErr)
	}
}

func TestGetWebServerConnWhenListenerNotSet(t *testing.T) {
	_, err := newBridge(nil, 30).WebServerConn(context.Background(), "127.0.0.1:8080")
	if err == nil || !strings.Contains(err.Error(), "web server not set") {
		t.Fatalf("WebServerConn() err=%v, want web server not set", err)
	}
}

func TestGetWebServerConnDialSuccess(t *testing.T) {
	l := conn.NewVirtualListener(conn.LocalTCPAddr)
	t.Cleanup(func() { _ = l.Close() })
	b := newBridge(nil, 30)
	b.SetWebListener(l)

	c, err := b.WebServerConn(context.Background(), "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("WebServerConn() err=%v, want nil", err)
	}
	defer func() { _ = c.Close() }()
	accepted, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept() err=%v, want nil", err)
	}
	defer func() { _ = accepted.Close() }()
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
