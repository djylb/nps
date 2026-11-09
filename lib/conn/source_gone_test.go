//go:build unix

package conn

import (
	"net"
	"testing"
	"time"
)

func TestSourceGone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	peer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if SourceGone(c) {
		t.Fatal("open connection reported gone")
	}
	if _, err := peer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	_ = peer.Close()
	time.Sleep(50 * time.Millisecond)
	if SourceGone(c) {
		t.Fatal("unread data reported gone")
	}
	b := make([]byte, 1)
	if _, err := c.Read(b); err != nil || b[0] != 'x' {
		t.Fatalf("peek took the data: %q %v", b, err)
	}
	if !SourceGone(c) {
		t.Fatal("closed peer not reported")
	}
}
