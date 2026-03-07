package rate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBytesToNsCeil(t *testing.T) {
	t.Parallel()

	if got := bytesToNsCeil(0, 100); got != 0 {
		t.Fatalf("bytesToNsCeil(0,100)=%d, want 0", got)
	}
	if got := bytesToNsCeil(100, 0); got != 0 {
		t.Fatalf("bytesToNsCeil(100,0)=%d, want 0", got)
	}
	if got := bytesToNsCeil(1, 2); got != 500000000 {
		t.Fatalf("bytesToNsCeil(1,2)=%d, want 500000000", got)
	}
	if got := bytesToNsCeil(3, 2); got != 1500000000 {
		t.Fatalf("bytesToNsCeil(3,2)=%d, want 1500000000", got)
	}
	if got := bytesToNsCeil(maxI64, 1); got != maxI64 {
		t.Fatalf("bytesToNsCeil(maxI64,1)=%d, want maxI64", got)
	}
}

func TestBytesPerSec(t *testing.T) {
	t.Parallel()

	if got := bytesPerSec(0, 100); got != 0 {
		t.Fatalf("bytesPerSec(0,100)=%d, want 0", got)
	}
	if got := bytesPerSec(100, 0); got != 0 {
		t.Fatalf("bytesPerSec(100,0)=%d, want 0", got)
	}
	if got := bytesPerSec(5, int64(time.Second)); got != 5 {
		t.Fatalf("bytesPerSec(5,1s)=%d, want 5", got)
	}
	if got := bytesPerSec(3, int64(2*time.Second)); got != 1 {
		t.Fatalf("bytesPerSec(3,2s)=%d, want 1", got)
	}
	if got := bytesPerSec(maxI64, 1); got != maxI64 {
		t.Fatalf("bytesPerSec(maxI64,1)=%d, want maxI64", got)
	}
}

func TestClampHelpers(t *testing.T) {
	t.Parallel()

	if got := clampAdd(1, 2); got != 3 {
		t.Fatalf("clampAdd(1,2)=%d, want 3", got)
	}
	if got := clampAdd(maxI64-1, 10); got != maxI64 {
		t.Fatalf("clampAdd overflow=%d, want maxI64", got)
	}
	if got := clampSub(5, 2); got != 3 {
		t.Fatalf("clampSub(5,2)=%d, want 3", got)
	}
	if got := clampSub(minI64+1, 10); got != minI64 {
		t.Fatalf("clampSub underflow=%d, want minI64", got)
	}
}

func TestRateLifecycleAndJSON(t *testing.T) {
	r := NewRate(1024)
	if r == nil {
		t.Fatal("NewRate returned nil")
	}
	if got := r.Limit(); got != 1024 {
		t.Fatalf("Limit()=%d, want 1024", got)
	}

	r.Stop()
	r.Get(1024)
	if got := r.Now(); got != 0 {
		t.Fatalf("Now() after Stop/Get=%d, want 0", got)
	}

	r.Start()
	r.SetLimit(2048)
	if got := r.Limit(); got != 2048 {
		t.Fatalf("Limit() after SetLimit=%d, want 2048", got)
	}

	r.ResetLimit(0)
	if got := r.Limit(); got != 0 {
		t.Fatalf("Limit() after ResetLimit(0)=%d, want 0", got)
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
	r.SetLimit(1)
	r.ResetLimit(1)
	r.Start()
	r.Stop()
	r.Get(1)
	r.ReturnBucket(1)

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
}
