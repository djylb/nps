package rate

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestMathHelpers(t *testing.T) {
	tests := []struct {
		name string
		got  int64
		want int64
	}{
		{name: "bytesToNsCeil basic", got: bytesToNsCeil(100, 50), want: 2_000_000_000},
		{name: "bytesToNsCeil invalid", got: bytesToNsCeil(0, 50), want: 0},
		{name: "bytesPerSec basic", got: bytesPerSec(500, 500_000_000), want: 1000},
		{name: "bytesPerSec invalid", got: bytesPerSec(500, 0), want: 0},
		{name: "clampAdd overflow", got: clampAdd(maxI64-1, 10), want: maxI64},
		{name: "clampSub underflow", got: clampSub(minI64+1, 10), want: minI64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestRateLimitLifecycle(t *testing.T) {
	r := NewRate(-1)
	if got := r.Limit(); got != 0 {
		t.Fatalf("negative limit should normalize to 0, got %d", got)
	}

	r.SetLimit(2048)
	if got := r.Limit(); got != 2048 {
		t.Fatalf("SetLimit failed, got %d", got)
	}

	r.Stop()
	if got := atomic.LoadInt32(&r.enabled); got != 0 {
		t.Fatalf("Stop should disable rate limiter, got enabled=%d", got)
	}

	r.ResetLimit(4096)
	if got := r.Limit(); got != 4096 {
		t.Fatalf("ResetLimit should update limit, got %d", got)
	}
	if got := atomic.LoadInt32(&r.enabled); got != 1 {
		t.Fatalf("ResetLimit should re-enable limiter, got enabled=%d", got)
	}
}

func TestRateNowSamplingAndReturnBucket(t *testing.T) {
	r := NewRate(0)
	r.Get(1000)

	atomic.StoreInt64(&r.lastSampleNs, r.nowNs()-sampleIntervalNs-1)
	if now := r.Now(); now <= 0 {
		t.Fatalf("expected positive sampled rate, got %d", now)
	}

	r.ReturnBucket(5000)
	atomic.StoreInt64(&r.lastSampleNs, r.nowNs()-sampleIntervalNs-1)
	if now := r.Now(); now != 0 {
		t.Fatalf("negative bucket refund should clamp sampled rate to 0, got %d", now)
	}
}

func TestMarshalJSON(t *testing.T) {
	r := NewRate(1234)
	data, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var payload map[string]int64
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json payload should be valid, got err=%v", err)
	}
	if payload["Limit"] != 1234 {
		t.Fatalf("unexpected limit field, got %d", payload["Limit"])
	}

	var nilRate *Rate
	data, err = nilRate.MarshalJSON()
	if err != nil {
		t.Fatalf("nil MarshalJSON returned error: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("nil rate should marshal to null, got %s", string(data))
	}
}

func TestStopUnblocksGet(t *testing.T) {
	r := NewRate(100)
	done := make(chan struct{})

	go func() {
		r.Get(1000)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	r.Stop()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Get should be unblocked soon after Stop")
	}
}
