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

type mockDialer struct {
	dialFn func(remote string) (net.Conn, error)
}

func resetLookup() {
	lookup = atomic.Value{}
}

func (m *mockDialer) DialVirtual(remote string) (net.Conn, error) {
	return m.dialFn(remote)
}

func (m *mockDialer) ServeVirtual(c net.Conn) {}

func TestGetTunnelConnWithoutLookup(t *testing.T) {
	resetLookup()
	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || err.Error() != "tunnel lookup not set" {
		t.Fatalf("GetTunnelConn() error = %v, want tunnel lookup not set", err)
	}
}

func TestGetTunnelConnTunnelNotFound(t *testing.T) {
	SetLookup(func(id int) (Dialer, bool) {
		return nil, false
	})

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || err.Error() != "tunnel not found" {
		t.Fatalf("GetTunnelConn() error = %v, want tunnel not found", err)
	}
}

func TestGetTunnelConnDialError(t *testing.T) {
	wantErr := errors.New("dial failed")
	SetLookup(func(id int) (Dialer, bool) {
		return &mockDialer{dialFn: func(remote string) (net.Conn, error) {
			if remote != "127.0.0.1:8080" {
				t.Fatalf("DialVirtual remote = %q, want 127.0.0.1:8080", remote)
			}
			return nil, wantErr
		}}, true
	})

	_, err := GetTunnelConn(9, "127.0.0.1:8080")
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetTunnelConn() error = %v, want %v", err, wantErr)
	}
}

func TestGetTunnelConnSuccess(t *testing.T) {
	SetLookup(func(id int) (Dialer, bool) {
		if id != 9 {
			t.Fatalf("lookup id = %d, want 9", id)
		}
		return &mockDialer{dialFn: func(remote string) (net.Conn, error) {
			if remote != "127.0.0.1:8081" {
				t.Fatalf("DialVirtual remote = %q, want 127.0.0.1:8081", remote)
			}
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				_, _ = server.Write([]byte("ok"))
			}()
			return client, nil
		}}, true
	})

	c, err := GetTunnelConn(9, "127.0.0.1:8081")
	if err != nil {
		t.Fatalf("GetTunnelConn() unexpected error: %v", err)
	}
	defer c.Close()

	buf, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("conn payload = %q, want ok", string(buf))
	}
}

func TestGetWebServerConnWithoutListener(t *testing.T) {
	old := WebServerListener
	WebServerListener = nil
	t.Cleanup(func() { WebServerListener = old })

	_, err := GetWebServerConn("127.0.0.1:80")
	if err == nil || err.Error() != "web server not set" {
		t.Fatalf("GetWebServerConn() error = %v, want web server not set", err)
	}
}

func TestGetWebServerConnInvalidRemote(t *testing.T) {
	old := WebServerListener
	WebServerListener = conn.NewVirtualListener(nil)
	t.Cleanup(func() {
		_ = WebServerListener.Close()
		WebServerListener = old
	})

	_, err := GetWebServerConn("invalid")
	if err == nil || !strings.Contains(err.Error(), "invalid remote addr") {
		t.Fatalf("GetWebServerConn() error = %v, want invalid remote addr", err)
	}
}

func TestGetWebServerConnSuccess(t *testing.T) {
	old := WebServerListener
	WebServerListener = conn.NewVirtualListener(nil)
	t.Cleanup(func() {
		_ = WebServerListener.Close()
		WebServerListener = old
	})

	acceptCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		c, err := WebServerListener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		acceptCh <- c
	}()

	clientConn, err := GetWebServerConn("127.0.0.1:9000")
	if err != nil {
		t.Fatalf("GetWebServerConn() error: %v", err)
	}
	defer clientConn.Close()

	var serverConn net.Conn
	select {
	case err := <-errCh:
		t.Fatalf("Accept() error: %v", err)
	case serverConn = <-acceptCh:
	}
	defer serverConn.Close()

	go func() {
		_, _ = clientConn.Write([]byte("pong"))
	}()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("server ReadFull() error: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("server received = %q, want pong", string(buf))
	}
}
