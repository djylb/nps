package client

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/model"
)

// stubVisitor serves every link it opens with fn, on the far end of a pipe.
type stubVisitor func(target string, c net.Conn)

func (f stubVisitor) OpenLink(_ context.Context, r coreapi.LinkRequest) (net.Conn, error) {
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		f(r.Target, b)
	}()
	return a, nil
}

func (stubVisitor) Close() error { return nil }

func assertEcho(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, len(msg))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != msg {
		t.Fatalf("echo %q, %v", b, err)
	}
}

func TestMixedServesHTTPAndSocks5(t *testing.T) {
	hosts := make(chan string, 2)
	mgr := NewSecretManager(t.Context(), func() {}, config.NewCommonConfig())
	t.Cleanup(mgr.Close)
	mgr.coreVisitor = func(_, _ string) coreapi.Visitor {
		return stubVisitor(func(target string, c net.Conn) {
			hosts <- target
			br := bufio.NewReader(c)
			if target == "web.test:80" {
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				_ = req.Body.Close()
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
				return
			}
			_, _ = io.Copy(c, br)
		})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := mgr.StartLocalServer(&config.LocalServer{Type: config.LocalMixed, ConnMode: config.ConnRelay, Ip: "127.0.0.1", Port: port, Password: "pw"}); err != nil {
		t.Fatalf("StartLocalServer() = %v", err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)

	hc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hc.Close() }()
	_ = hc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(hc, "GET http://web.test/ HTTP/1.1\r\nHost: web.test\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(hc), nil)
	if err != nil {
		t.Fatalf("http proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("http proxy answered %d %q", resp.StatusCode, body)
	}
	if h := <-hosts; h != "web.test:80" {
		t.Fatalf("http proxy link host = %q", h)
	}

	sc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sc.Close() }()
	_ = sc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = sc.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err := io.ReadFull(sc, reply); err != nil || reply[1] != 0 {
		t.Fatalf("socks5 method reply %v, %v", reply, err)
	}
	_, _ = sc.Write([]byte{5, 1, 0, 1, 10, 0, 0, 1, 0, 80})
	connect := make([]byte, 10)
	if _, err := io.ReadFull(sc, connect); err != nil || connect[1] != 0 {
		t.Fatalf("socks5 connect reply %v, %v", connect, err)
	}
	assertEcho(t, sc, "socks")
	if h := <-hosts; h != "10.0.0.1:80" {
		t.Fatalf("socks5 link host = %q", h)
	}
}

func TestP2PLocalAddr(t *testing.T) {
	for in, want := range map[string]string{
		"":                "invalid IP",
		"0.0.0.0":         "invalid IP",
		"[::]":            "invalid IP",
		"[2001:db8::10]":  "2001:db8::10",
		" 192.168.1.10 ":  "192.168.1.10",
		"::ffff:10.0.0.1": "10.0.0.1",
	} {
		if got := p2pLocalAddr(in).String(); got != want {
			t.Fatalf("p2pLocalAddr(%q) = %s, want %s", in, got, want)
		}
	}
}

// [common] compress is what the visitor asks of its nested streams, and the
// listener does not compress on its own.
func TestLocalCompressAsksNestedStream(t *testing.T) {
	cfg := config.NewCommonConfig()
	cfg.Client.Cnf.Compress = true
	asked := make(chan []string, 1)
	mgr := NewSecretManager(t.Context(), func() {}, cfg)
	t.Cleanup(mgr.Close)
	mgr.coreVisitor = func(_, _ string) coreapi.Visitor {
		return compressVisitor{asked}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := mgr.StartLocalServer(&config.LocalServer{Type: config.LocalTCP, ConnMode: config.ConnRelay, Ip: "127.0.0.1", Port: port, Password: "pw", Target: "10.0.0.1:80"}); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	assertEcho(t, c, "plain bytes")
	if got := <-asked; !slices.Equal(got, []string{model.CompressSnappy}) {
		t.Fatalf("asked compression %v", got)
	}
}

type compressVisitor struct{ asked chan<- []string }

func (v compressVisitor) OpenLink(_ context.Context, r coreapi.LinkRequest) (net.Conn, error) {
	v.asked <- r.Compress
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		_, _ = io.Copy(b, b)
	}()
	return a, nil
}

func (compressVisitor) Close() error { return nil }

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func mixedProxy(t *testing.T, accounts map[string]string) string {
	t.Helper()
	return typedProxy(t, config.LocalMixed, accounts)
}

func typedProxy(t *testing.T, typ string, accounts map[string]string) string {
	t.Helper()
	mgr := NewSecretManager(t.Context(), func() {}, config.NewCommonConfig())
	t.Cleanup(mgr.Close)
	mgr.coreVisitor = func(_, _ string) coreapi.Visitor {
		return stubVisitor(func(_ string, c net.Conn) {
			req, err := http.ReadRequest(bufio.NewReader(c))
			if err != nil {
				return
			}
			_ = req.Body.Close()
			_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		})
	}
	port := freePort(t)
	if err := mgr.StartLocalServer(&config.LocalServer{Type: typ, Ip: "127.0.0.1", Port: port, Auth: accounts}); err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// socks5Connect does a SOCKS5 handshake and reports whether it was accepted.
func socks5Connect(t *testing.T, addr, user, pass string) bool {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	method := byte(0)
	if user != "" {
		method = 2
	}
	_, _ = c.Write([]byte{5, 1, method})
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil || reply[1] != method {
		return false
	}
	if user != "" {
		_, _ = c.Write(append(append(append([]byte{1, byte(len(user))}, user...), byte(len(pass))), pass...))
		if _, err := io.ReadFull(c, reply); err != nil || reply[1] != 0 {
			return false
		}
	}
	_, _ = c.Write([]byte{5, 1, 0, 3, 8, 'w', 'e', 'b', '.', 't', 'e', 's', 't', 0, 80})
	head := make([]byte, 2)
	_, err = io.ReadFull(c, head)
	return err == nil && head[1] == 0
}

// httpStatus sends a proxy request with the given Basic credentials.
func httpStatus(t *testing.T, addr, user, pass string) int {
	t.Helper()
	proxy, _ := url.Parse("http://" + addr)
	if user != "" {
		proxy.User = url.UserPassword(user, pass)
	}
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	defer c.CloseIdleConnections()
	resp, err := c.Get("http://web.test/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestMixedAccounts(t *testing.T) {
	addr := mixedProxy(t, map[string]string{"a": "1", "b": "2"})
	for _, tc := range []struct {
		user, pass string
		ok         bool
	}{{"a", "1", true}, {"b", "2", true}, {"a", "2", false}, {"c", "1", false}, {"", "", false}} {
		if got := socks5Connect(t, addr, tc.user, tc.pass); got != tc.ok {
			t.Errorf("socks5 %q: %v", tc.user, got)
		}
		if got := httpStatus(t, addr, tc.user, tc.pass) == http.StatusOK; got != tc.ok {
			t.Errorf("http %q: %v", tc.user, got)
		}
	}
	open := mixedProxy(t, nil)
	if !socks5Connect(t, open, "", "") || httpStatus(t, open, "", "") != http.StatusOK {
		t.Error("a listener without accounts refused a visitor")
	}
}

// socks5 and http serve one protocol of the mixed listener, with its accounts.
func TestSingleProtocolListeners(t *testing.T) {
	acc := map[string]string{"a": "1"}
	socks := typedProxy(t, config.LocalSOCKS5, acc)
	if !socks5Connect(t, socks, "a", "1") || socks5Connect(t, socks, "a", "2") {
		t.Error("socks5 listener accounts")
	}
	if c, err := net.DialTimeout("tcp", socks, 3*time.Second); err == nil {
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = io.WriteString(c, "GET http://web.test/ HTTP/1.1\r\nHost: web.test\r\n\r\n")
		if n, _ := io.Copy(io.Discard, c); n != 0 {
			t.Error("socks5 listener answered an HTTP proxy request")
		}
	}
	web := typedProxy(t, config.LocalHTTP, acc)
	if httpStatus(t, web, "a", "1") != http.StatusOK {
		t.Error("http listener refused a valid account")
	}
	if httpStatus(t, web, "a", "2") == http.StatusOK {
		t.Error("http listener accepted a wrong password")
	}
	if socks5Connect(t, web, "a", "1") {
		t.Error("http listener served SOCKS5")
	}
}
