package conn

import (
	"net"
	"testing"
	"time"

	"github.com/xtaci/kcp-go/v5"
)

func TestUDPSplitByFirstPacket(t *testing.T) {
	s, err := ListenUDPSplit("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	addr := s.pc.LocalAddr().String()
	send := func(b []byte) {
		c, err := net.Dial("udp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		_, _ = c.Write(b)
	}
	read := func(c *splitSide) bool {
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, _, err := c.ReadFrom(make([]byte, 1500))
		return err == nil
	}
	send([]byte{1, 2, 3, 4, 0xf1, 0, 9})
	send([]byte{1, 2, 3, 4, 0xf2, 0, 9})
	if !read(s.KCP) || !read(s.KCP) || read(s.QUIC) {
		t.Fatal("FEC packets not all KCP's")
	}
	send([]byte{0xc0, 0, 0, 0, 1, 8, 9})
	if !read(s.QUIC) || read(s.KCP) {
		t.Fatal("long header not QUIC's")
	}
	// an unblocked read ends with the deadline, a closed side with ErrClosed
	_ = s.KCP.SetReadDeadline(time.Time{})
	_ = s.KCP.Close()
	if _, _, err := s.KCP.ReadFrom(nil); err != net.ErrClosed {
		t.Fatal(err)
	}
}

// A KCP session as ListenKCP serves it works over the KCP side, and the
// listener ends with the split.
func TestUDPSplitKCPSession(t *testing.T) {
	s, err := ListenUDPSplit("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := ListenKCPOn(s.KCP)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		c, err := l.AcceptKCP()
		if err != nil {
			return
		}
		b := make([]byte, 5)
		n, _ := c.Read(b)
		got <- string(b[:n])
	}()
	c, err := kcp.DialWithOptions(s.pc.LocalAddr().String(), nil, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("hello"))
	select {
	case v := <-got:
		if v != "hello" {
			t.Fatal(v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no data over the KCP side")
	}
	_ = l.Close()
	_ = s.Close()
}
