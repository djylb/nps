package client

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
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
