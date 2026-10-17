package client

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGetProxyConnHttpSendsProxyAuthorization(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	got := make(chan *http.Request, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil {
			return
		}
		got <- req
		_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	}()

	c, err := GetProxyConn("http://user:pass@"+ln.Addr().String(), "example.com:443", 2*time.Second, "")
	if err != nil {
		t.Fatalf("GetProxyConn: %v", err)
	}
	_ = c.Close()

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

func TestGetProxyConnTimesOutOnSilentProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan net.Conn, 2)
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

	for _, scheme := range []string{"http", "socks5"} {
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
