package rate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBytesToNsCeil(t *testing.T) {
	if got := bytesToNsCeil(0, 100); got != 0 {
		t.Fatalf("bytes=0 should return 0, got %d", got)
	}
	if got := bytesToNsCeil(100, 0); got != 0 {
		t.Fatalf("rate=0 should return 0, got %d", got)
	}
	if got := bytesToNsCeil(100, 50); got != 2_000_000_000 {
		t.Fatalf("unexpected ceil conversion, got %d", got)
	}
	if got := bytesToNsCeil(maxI64, 1); got != maxI64 {
		t.Fatalf("overflow should clamp to maxI64, got %d", got)
	}
}

func TestBytesPerSec(t *testing.T) {
	if got := bytesPerSec(0, 100); got != 0 {
		t.Fatalf("bytes=0 should return 0, got %d", got)
	}
	if got := bytesPerSec(100, 0); got != 0 {
		t.Fatalf("dt=0 should return 0, got %d", got)
	}
	if got := bytesPerSec(100, int64(time.Second)); got != 100 {
		t.Fatalf("unexpected bytes/sec, got %d", got)
	}
	if got := bytesPerSec(maxI64, 1); got != maxI64 {
		t.Fatalf("overflow should clamp to maxI64, got %d", got)
	}
}

func TestClampOperations(t *testing.T) {
	if got := clampAdd(10, 2); got != 12 {
		t.Fatalf("unexpected clampAdd result: %d", got)
	}
	if got := clampAdd(maxI64-1, 10); got != maxI64 {
		t.Fatalf("clampAdd should clamp to maxI64, got %d", got)
	}
	if got := clampSub(10, 2); got != 8 {
		t.Fatalf("unexpected clampSub result: %d", got)
	}
	if got := clampSub(minI64+1, 10); got != minI64 {
		t.Fatalf("clampSub should clamp to minI64, got %d", got)
	}
}

func TestRateStartStopAndMarshalJSON(t *testing.T) {
	r := NewRate(1024)
	r.Get(128)
	r.Stop()

	if got := r.Now(); got != 0 {
		t.Fatalf("Now should be reset to 0 after Stop, got %d", got)
	}

	r.Start()
	if got := r.Limit(); got != 1024 {
		t.Fatalf("unexpected limit after Start: %d", got)
	}

	data, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	var payload map[string]int64
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if payload["Limit"] != 1024 {
		t.Fatalf("unexpected Limit in JSON: %d", payload["Limit"])
	}
}

func TestRateResetLimit(t *testing.T) {
	r := NewRate(2048)
	r.ResetLimit(512)
	if got := r.Limit(); got != 512 {
		t.Fatalf("unexpected limit after ResetLimit: %d", got)
	}
}
