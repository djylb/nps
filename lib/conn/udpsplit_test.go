package conn

import (
	"net"
	"testing"
	"time"

	"container/list"
	"github.com/xtaci/kcp-go/v5"
	"net/netip"
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

// A full table takes a new source in place of the one silent longest.
func TestUDPSplitFullTableEvictsOldest(t *testing.T) {
	s := &UDPSplit{src: map[netip.AddrPort]*list.Element{}}
	now := time.Now()
	addr := func(i int) netip.AddrPort {
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}), 1)
	}
	for i := range splitMax {
		s.classify(addr(i), false, now)
	}
	s.classify(addr(0), false, now.Add(time.Second)) // seen again: no longer the oldest
	if !s.classify(addr(splitMax), true, now.Add(2*time.Second)) {
		t.Fatal("new source not told apart")
	}
	if s.src[addr(splitMax)] == nil || len(s.src) != splitMax {
		t.Fatalf("new source not kept, %d entries", len(s.src))
	}
	if s.src[addr(1)] != nil || s.src[addr(0)] == nil {
		t.Fatal("evicted the wrong source")
	}
	if s.order.Len() != len(s.src) {
		t.Fatalf("order %d, table %d", s.order.Len(), len(s.src))
	}
}

func TestUDPSplitExpiresIdle(t *testing.T) {
	s := &UDPSplit{src: map[netip.AddrPort]*list.Element{}}
	now := time.Now()
	old := netip.MustParseAddrPort("10.0.0.1:1")
	s.classify(old, false, now)
	s.classify(netip.MustParseAddrPort("10.0.0.2:1"), false, now.Add(splitIdle))
	s.expire(now.Add(splitIdle + time.Second))
	if s.src[old] != nil || len(s.src) != 1 {
		t.Fatalf("%d entries", len(s.src))
	}
}
