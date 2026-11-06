package rate

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

func TestRateLifecycleAndJSON(t *testing.T) {
	r := NewRate(1024)
	if r == nil {
		t.Fatal("NewRate returned nil")
	}
	if got := r.Limit(); got != 1024 {
		t.Fatalf("Limit()=%d, want 1024", got)
	}

	r.Stop()
	c := NewRateConn(discardRWC{}, r)
	if _, err := c.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := r.Now(); got != 0 {
		t.Fatalf("Now() after Stop/Write=%d, want 0", got)
	}

	r.Start()
	r.ResetLimit(2048)
	if got := r.Limit(); got != 2048 {
		t.Fatalf("Limit() after ResetLimit=%d, want 2048", got)
	}

	r.ResetLimit(-1)
	if got := r.Limit(); got != 0 {
		t.Fatalf("Limit() after ResetLimit(-1)=%d, want 0", got)
	}

	b, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var out map[string]int64
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if out["Limit"] != 0 {
		t.Fatalf("MarshalJSON Limit=%d, want 0", out["Limit"])
	}
}

func TestNilRateSafety(t *testing.T) {
	var r *Rate
	r.ResetLimit(1)
	r.Start()
	r.Stop()

	if got := r.Limit(); got != 0 {
		t.Fatalf("nil Limit()=%d, want 0", got)
	}
	if got := r.Now(); got != 0 {
		t.Fatalf("nil Now()=%d, want 0", got)
	}
	b, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("nil MarshalJSON error: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("nil MarshalJSON=%s, want null", string(b))
	}
	SetGlobalTraffic(0, 0)
	t.Cleanup(ResetGlobalTraffic)
	c := NewRateConn(discardRWC{}, nil)
	if _, err := c.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if in, _ := GlobalTraffic(); in != 100 {
		t.Fatalf("a nil Rate left the global traffic uncounted: in = %d", in)
	}
}

func TestNewRateConnKeepsNetConn(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	c := NewRateConn(a, NewRate(0))
	defer func() { _ = c.Close() }()
	if _, ok := c.(net.Conn); !ok {
		t.Fatalf("NewRateConn(net.Conn) = %T, want a net.Conn", c)
	}
}

type discardRWC struct{}

func (discardRWC) Read([]byte) (int, error)    { return 0, nil }
func (discardRWC) Write(b []byte) (int, error) { return len(b), nil }
func (discardRWC) Close() error                { return nil }

func TestRateConnCloseWakesWaitingWrite(t *testing.T) {
	t.Parallel()

	c := NewRateConn(discardRWC{}, NewRate(1))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Write(make([]byte, 1024))
	}()
	time.Sleep(50 * time.Millisecond)
	_ = c.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write still waiting for rate budget after Close")
	}
}

// TestRateConnSharesOneBucket checks that reads and writes on a connection
// draw from the same limit.
func TestRateConnSharesOneBucket(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		peer, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = peer.Close() }()
		_, _ = io.Copy(struct{ io.Writer }{peer}, peer)
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	const limit = 4000
	c := NewRateConn(raw, NewRate(limit))
	defer func() { _ = c.Close() }()

	// The write takes the whole initial burst of two seconds' worth, so
	// reading back half of it has to wait about a second for the limit.
	if _, err := c.Write(make([]byte, 2*limit)); err != nil {
		t.Fatalf("write: %v", err)
	}
	start := time.Now()
	if _, err := io.ReadFull(c, make([]byte, limit/2)); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Fatalf("reading %d bytes at %d B/s after a write spent the burst took %v; reads are not charged to the write's limit", limit/2, limit, d)
	}
}
