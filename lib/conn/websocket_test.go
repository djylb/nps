package conn

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/netx/httphead"
	"github.com/djylb/netx/wsconn"
)

func TestWSListenerRoundTripAndRealIP(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := NewWSListener(base, "/ws", "127.0.0.1", "X-Real-IP", nil)
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = c.Close() }()
		if got := c.RemoteAddr().String(); got != "192.0.2.7:0" {
			t.Errorf("RemoteAddr = %q", got)
		}
		_, err = io.Copy(c, io.LimitReader(c, 10))
		done <- err
	}()

	raw, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// The listener trusts 127.0.0.1, so the X-Real-IP it sends is believed.
	ws, _, err := wsconn.Client(context.Background(), raw, "ws://"+base.Addr().String()+"/ws",
		&wsconn.ClientOptions{Header: httphead.Header{{Name: "X-Real-IP", Value: "192.0.2.7"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := wsconn.NewStreamConn(ws)
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write([]byte("01234")); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write([]byte("56789")); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(io.LimitReader(bufio.NewReader(c), 10))
	if err != nil || string(got) != "0123456789" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWSRoutePaths(t *testing.T) {
	var wrapped, extra atomic.Int32
	mark := func(n *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1); w.WriteHeader(http.StatusTeapot) })
	}
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	route := &WSRoute{
		Wrap:  func(http.Handler) http.Handler { return mark(&wrapped) },
		Paths: map[string]http.Handler{"/x": mark(&extra), "bad path {": mark(&extra)},
	}
	ln := NewWSListener(base, "/ws", "", "", route)
	defer func() { _ = ln.Close() }()
	for path, want := range map[string]int{"/ws": http.StatusTeapot, "/x": http.StatusTeapot, "/other": http.StatusNotFound} {
		resp, err := http.Get("http://" + base.Addr().String() + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	if wrapped.Load() != 1 || extra.Load() != 1 {
		t.Fatalf("wrapped %d, extra %d", wrapped.Load(), extra.Load())
	}
}
