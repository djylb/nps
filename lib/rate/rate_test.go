package rate

import (
	"encoding/json"
	"math"
	"sync/atomic"
	"testing"
)

func TestBytesToNsCeil(t *testing.T) {
	tests := []struct {
		name string
		b    int64
		r    int64
		want int64
	}{
		{name: "invalid bytes", b: 0, r: 100, want: 0},
		{name: "invalid rate", b: 10, r: 0, want: 0},
		{name: "normal ceil", b: 3, r: 2, want: 1500000000},
		{name: "overflow clamp", b: maxI64, r: 1, want: maxI64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bytesToNsCeil(tt.b, tt.r); got != tt.want {
				t.Fatalf("bytesToNsCeil(%d,%d)=%d, want %d", tt.b, tt.r, got, tt.want)
			}
		})
	}
}

func TestBytesPerSec(t *testing.T) {
	tests := []struct {
		name string
		b    int64
		dt   int64
		want int64
	}{
		{name: "invalid bytes", b: 0, dt: 1, want: 0},
		{name: "invalid interval", b: 1, dt: 0, want: 0},
		{name: "normal", b: 3, dt: 2, want: 1500000000},
		{name: "large clamped", b: maxI64, dt: 1, want: maxI64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bytesPerSec(tt.b, tt.dt); got != tt.want {
				t.Fatalf("bytesPerSec(%d,%d)=%d, want %d", tt.b, tt.dt, got, tt.want)
			}
		})
	}
}

func TestClampAddAndClampSub(t *testing.T) {
	if got := clampAdd(10, 5); got != 15 {
		t.Fatalf("clampAdd normal=%d, want 15", got)
	}
	if got := clampAdd(maxI64-1, 10); got != maxI64 {
		t.Fatalf("clampAdd overflow=%d, want %d", got, maxI64)
	}
	if got := clampSub(10, 5); got != 5 {
		t.Fatalf("clampSub normal=%d, want 5", got)
	}
	if got := clampSub(minI64+1, 10); got != minI64 {
		t.Fatalf("clampSub underflow=%d, want %d", got, minI64)
	}
}

func TestRateLifecycleAndMarshalJSON(t *testing.T) {
	r := NewRate(1024)
	if r.Limit() != 1024 {
		t.Fatalf("initial limit=%d, want 1024", r.Limit())
	}

	r.SetLimit(-1)
	if r.Limit() != 0 {
		t.Fatalf("negative limit should become 0, got %d", r.Limit())
	}

	r.Stop()
	if atomic.LoadInt32(&r.enabled) != 0 {
		t.Fatalf("Stop should disable limiter")
	}

	r.Start()
	if atomic.LoadInt32(&r.enabled) != 1 {
		t.Fatalf("Start should enable limiter")
	}

	r.ResetLimit(2048)
	if r.Limit() != 2048 {
		t.Fatalf("reset limit=%d, want 2048", r.Limit())
	}

	body, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var payload map[string]int64
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if payload["Limit"] != 2048 {
		t.Fatalf("json limit=%d, want 2048", payload["Limit"])
	}
}

func TestRateGetAndReturnBucket(t *testing.T) {
	r := NewRate(math.MaxInt32)

	r.Get(100)
	atomic.StoreInt64(&r.lastSampleNs, r.nowNs()-sampleIntervalNs)
	if now := r.Now(); now <= 0 {
		t.Fatalf("Now should report positive rate after Get, got %d", now)
	}

	r.ReturnBucket(100)
	atomic.StoreInt64(&r.lastSampleNs, r.nowNs()-sampleIntervalNs)
	if now := r.Now(); now != 0 {
		t.Fatalf("Now should not be negative after ReturnBucket, got %d", now)
	}
}
