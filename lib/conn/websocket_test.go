package conn

import (
	"bufio"
	"context"
	"io"
	"net"
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
	ln := NewWSListener(base, "/ws", "127.0.0.1", "X-Real-IP")
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
