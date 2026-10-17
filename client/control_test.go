package client

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/djylb/netx/socks5"
)

// serveOnce accepts one connection on a new listener and runs handle on it.
func serveOnce(t *testing.T, handle func(net.Conn)) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		handle(c)
	}()
	return ln
}

// httpProxy answers one CONNECT request, sends tunnel bytes right after its
// reply, as a proxy may, and then echoes the tunnel.
func httpProxy(t *testing.T, got chan<- *http.Request) net.Listener {
	return serveOnce(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		got <- req
		_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\nhello"))
		_, _ = io.Copy(c, br)
	})
}

// socks5Proxy answers one no-auth SOCKS5 CONNECT request, reports the
// requested address and then echoes the tunnel.
func socks5Proxy(t *testing.T, got chan<- socks5.Addr) net.Listener {
	return serveOnce(t, func(c net.Conn) {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
			return
		}
		_, _ = c.Write([]byte{5, 0})
		req := make([]byte, 3)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		dst, err := socks5.ReadAddr(c)
		if err != nil {
			return
		}
		got <- dst
		_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		_, _ = io.Copy(struct{ io.Writer }{c}, c)
	})
}

// checkTunnel checks that c carries bytes both ways and, when want is not
// empty, that it starts with want.
func checkTunnel(t *testing.T, c net.Conn, want string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if want != "" {
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != want {
			t.Fatalf("tunnel prefix = %q, %v; want %q", buf, err, want)
		}
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("tunnel write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("tunnel echo = %q, %v", buf, err)
	}
}

func TestGetProxyConnHttp(t *testing.T) {
	got := make(chan *http.Request, 1)
	ln := httpProxy(t, got)

	c, err := GetProxyConn("http://user:pass@"+ln.Addr().String(), "example.com:443", 2*time.Second, "")
	if err != nil {
		t.Fatalf("GetProxyConn: %v", err)
	}
	defer func() { _ = c.Close() }()
	// The bytes sent with the CONNECT reply belong to the tunnel.
	checkTunnel(t, c, "hello")

	req := <-got
	if req.Method != http.MethodConnect || req.Host != "example.com:443" {
		t.Fatalf("unexpected request %s %s", req.Method, req.Host)
	}
	if v := req.Header.Get("Proxy-Authorization"); v != "Basic dXNlcjpwYXNz" {
		t.Fatalf("Proxy-Authorization = %q", v)
	}
	if v := req.Header.Get("Authorization"); v != "" {
		t.Fatalf("unexpected Authorization header %q", v)
	}
}

func TestGetProxyConnWithoutSchemeIsHttp(t *testing.T) {
	got := make(chan *http.Request, 1)
	ln := httpProxy(t, got)

	c, err := GetProxyConn(ln.Addr().String(), "example.com:443", 2*time.Second, "")
	if err != nil {
		t.Fatalf("GetProxyConn: %v", err)
	}
	defer func() { _ = c.Close() }()
	checkTunnel(t, c, "hello")
	if req := <-got; req.Method != http.MethodConnect || req.Host != "example.com:443" {
		t.Fatalf("unexpected request %s %s", req.Method, req.Host)
	}
}

func TestGetProxyConnSocks5(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			got := make(chan socks5.Addr, 1)
			ln := socks5Proxy(t, got)

			c, err := GetProxyConn(scheme+"://"+ln.Addr().String(), "example.com:443", 2*time.Second, "")
			if err != nil {
				t.Fatalf("GetProxyConn: %v", err)
			}
			defer func() { _ = c.Close() }()
			checkTunnel(t, c, "")
			// Both schemes leave the name to the proxy.
			if dst := <-got; dst.String() != "example.com:443" {
				t.Fatalf("requested %s, want example.com:443", dst)
			}
		})
	}
}

func TestGetProxyConnEnvironment(t *testing.T) {
	got := make(chan socks5.Addr, 1)
	ln := socks5Proxy(t, got)
	target := serveOnce(t, func(c net.Conn) { _, _ = io.Copy(struct{ io.Writer }{c}, c) })

	// Only the upper-case names: Windows treats the lower-case ones as the
	// same variables, and the upper-case ones take precedence elsewhere.
	t.Setenv("ALL_PROXY", "socks5://"+ln.Addr().String())
	t.Setenv("NO_PROXY", "127.0.0.1")

	// example.com goes through ALL_PROXY.
	c, err := GetProxyConn("", "example.com:443", 2*time.Second, "")
	if err != nil {
		t.Fatalf("GetProxyConn via ALL_PROXY: %v", err)
	}
	checkTunnel(t, c, "")
	_ = c.Close()
	if dst := <-got; dst.String() != "example.com:443" {
		t.Fatalf("requested %s, want example.com:443", dst)
	}

	// 127.0.0.1 is in NO_PROXY and is dialed directly.
	c, err = GetProxyConn("", target.Addr().String(), 2*time.Second, "")
	if err != nil {
		t.Fatalf("GetProxyConn bypassing ALL_PROXY: %v", err)
	}
	defer func() { _ = c.Close() }()
	checkTunnel(t, c, "")
	if c.RemoteAddr().String() != target.Addr().String() {
		t.Fatalf("dialed %s, want %s", c.RemoteAddr(), target.Addr())
	}
}

func TestGetProxyConnTimesOutOnSilentProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c // hold the connection open without answering
		}
	}()
	defer func() {
		for {
			select {
			case c := <-accepted:
				_ = c.Close()
			default:
				return
			}
		}
	}()

	for _, scheme := range []string{"http", "https", "socks5", "socks5h", "socks4a"} {
		start := time.Now()
		_, err := GetProxyConn(scheme+"://"+ln.Addr().String(), "example.com:443", 200*time.Millisecond, "")
		if err == nil {
			t.Fatalf("%s: expected an error from a silent proxy", scheme)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("%s: proxy handshake not bounded by timeout, took %v", scheme, d)
		}
	}
}
