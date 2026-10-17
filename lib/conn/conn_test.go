package conn

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type overlapDetectConn struct {
	active   atomic.Int32
	overlaps atomic.Int32
}

func (c *overlapDetectConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (c *overlapDetectConn) Close() error                       { return nil }
func (c *overlapDetectConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *overlapDetectConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *overlapDetectConn) SetDeadline(_ time.Time) error      { return nil }
func (c *overlapDetectConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *overlapDetectConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *overlapDetectConn) Write(b []byte) (int, error) {
	if !c.active.CompareAndSwap(0, 1) {
		c.overlaps.Add(1)
	}
	time.Sleep(10 * time.Millisecond)
	c.active.Store(0)
	return len(b), nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func TestConnWriteSerializesConcurrentWriters(t *testing.T) {
	raw := &overlapDetectConn{}
	c := NewConn(raw)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := c.Write([]byte("payload")); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if overlaps := raw.overlaps.Load(); overlaps != 0 {
		t.Fatalf("concurrent writes overlapped %d times", overlaps)
	}
}

func TestResolveUDPBindTargetHonorsSpecificAndWildcardHosts(t *testing.T) {
	tests := []struct {
		addr        string
		wantNetwork string
		wantBind    string
		wantExact   bool
	}{
		{addr: ":0", wantExact: false},
		{addr: "127.0.0.1:0", wantNetwork: "udp4", wantBind: "127.0.0.1:0", wantExact: true},
		{addr: "0.0.0.0:0", wantNetwork: "udp4", wantBind: "0.0.0.0:0", wantExact: true},
		{addr: "[::1]:0", wantNetwork: "udp6", wantBind: "[::1]:0", wantExact: true},
		{addr: "[::]:0", wantNetwork: "udp6", wantBind: "[::]:0", wantExact: true},
	}
	for _, tt := range tests {
		_, gotNetwork, gotBind, gotExact, err := resolveUDPBindTarget(tt.addr)
		if err != nil {
			t.Fatalf("resolveUDPBindTarget(%q) error = %v", tt.addr, err)
		}
		if gotExact != tt.wantExact || gotNetwork != tt.wantNetwork || gotBind != tt.wantBind {
			t.Fatalf("resolveUDPBindTarget(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.addr, gotNetwork, gotBind, gotExact, tt.wantNetwork, tt.wantBind, tt.wantExact)
		}
	}
}

func TestGetHostKeepsBytesAfterHeader(t *testing.T) {
	tests := []struct {
		name, header, rest, addr string
	}{
		{"post with body", "POST http://example.com/svn HTTP/1.1\r\nHost: example.com\r\nContent-Length: 5\r\n\r\n", "hello", "example.com:80"},
		{"connect with early data", "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n", "\x16\x03\x01", "example.com:443"},
		{"pipelined", "GET http://[::1]/ HTTP/1.1\r\nHost: [::1]\r\n\r\n", "GET http://[::1]/b HTTP/1.1\r\nHost: [::1]\r\n\r\n", "[::1]:80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer func() { _ = client.Close() }()
			defer func() { _ = server.Close() }()
			// Header and trailing bytes arrive in one write and nothing follows.
			go func() { _, _ = client.Write([]byte(tt.header + tt.rest)) }()

			c := NewConn(server)
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			addr, r, head, rest, err := c.GetHost()
			if err != nil {
				t.Fatalf("GetHost() error = %v", err)
			}
			if addr != tt.addr || r == nil {
				t.Fatalf("GetHost() addr = %q, want %q", addr, tt.addr)
			}
			if string(head) != tt.header || string(rest) != tt.rest {
				t.Fatalf("GetHost() head = %q rest = %q, want %q and %q", head, rest, tt.header, tt.rest)
			}
		})
	}
}
