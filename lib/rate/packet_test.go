package rate

import (
	"net"
	"testing"
	"time"
)

func TestVisitorPacketConnDropsOverLimit(t *testing.T) {
	r := NewRate(1000)
	r.Start()
	defer r.Stop()

	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recv.Close() }()
	send, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pc := NewVisitorPacketConn(send, r)
	defer func() { _ = pc.Close() }()

	// 10 KB at 1000 B/s is far over the burst: the writes must not wait,
	// and most datagrams must be dropped.
	start := time.Now()
	for range 10 {
		if n, err := pc.WriteTo(make([]byte, 1000), recv.LocalAddr()); n != 1000 || err != nil {
			t.Fatalf("WriteTo = %d, %v", n, err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("writes waited %v for the limit", d)
	}
	got := 0
	buf := make([]byte, 2000)
	for {
		_ = recv.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if _, _, err := recv.ReadFrom(buf); err != nil {
			break
		}
		got++
	}
	if got == 0 || got >= 10 {
		t.Fatalf("received %d datagrams, want some but not all", got)
	}
}
