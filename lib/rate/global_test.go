package rate

import (
	"net"
	"testing"
	"time"
)

func TestGlobalTraffic(t *testing.T) {
	SetGlobalTraffic(0, 0)
	t.Cleanup(ResetGlobalTraffic)
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	c := NewRateConn(a, NewRate(0)).(net.Conn)
	defer func() { _ = c.Close() }()
	go func() {
		buf := make([]byte, 64)
		_, _ = b.Read(buf)
		_, _ = b.Write(make([]byte, 30))
	}()
	if _, err := c.Write(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if n, err := c.Read(buf); err != nil || n != 30 {
		t.Fatalf("Read = %d, %v", n, err)
	}
	if in, out := GlobalTraffic(); in != 64 || out != 30 {
		t.Fatalf("GlobalTraffic = %d/%d, want 64 in to the npc and 30 out to visitors", in, out)
	}

	SetGlobalTraffic(1000, 2000)
	if in, out := GlobalTraffic(); in != 1000 || out != 2000 {
		t.Fatalf("restored traffic = %d/%d", in, out)
	}
	ResetGlobalTraffic()
	if in, out := GlobalTraffic(); in != 0 || out != 0 {
		t.Fatalf("reset traffic = %d/%d", in, out)
	}
}

func TestGlobalLimit(t *testing.T) {
	SetGlobalLimit(64 << 10)
	t.Cleanup(func() { SetGlobalLimit(0) })
	if GlobalLimit() != 64<<10 {
		t.Fatalf("GlobalLimit = %d", GlobalLimit())
	}
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	// a client without its own limit is held to the global one
	c := NewRateConn(a, NewRate(0)).(net.Conn)
	defer func() { _ = c.Close() }()
	start := time.Now()
	data := make([]byte, 32<<10)
	for range 8 {
		if _, err := c.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	// 256 KiB at 64 KiB/s after the burst takes well over a second
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("256 KiB passed in %v under a 64 KiB/s global limit", d)
	}
}
