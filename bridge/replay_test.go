package bridge

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAuthTimestampWindow(t *testing.T) {
	for _, tc := range []struct {
		ts, now, window int64
		valid           bool
	}{
		{700, 1000, 300, true}, {1300, 1000, 300, true},
		{699, 1000, 300, false}, {1301, 1000, 300, false},
		{0, 1000, 0, true}, {math.MaxInt64, 1000, 0, true},
		{math.MinInt64, 1000, 300, false}, {math.MaxInt64, 1000, 300, false},
		{math.MaxInt64, math.MinInt64, 300, false},
		{990, 1000, 10, true}, {989, 1000, 10, false},
	} {
		if got := authTimestampValid(tc.ts, tc.now, tc.window); got != tc.valid {
			t.Fatalf("timestamp=%d now=%d window=%d: got %v", tc.ts, tc.now, tc.window, got)
		}
	}
}

func TestReplayCoversEntireTimestampWindow(t *testing.T) {
	for _, window := range []int64{1, 30, 300, 600} {
		r := replay{items: make(map[string]int64)}
		const received int64 = 1000
		ts := received + window
		if !authTimestampValid(ts, received, window) || r.seen("key", received, window) {
			t.Fatal("first request rejected")
		}
		// A future-dated request remains valid until twice the window after receipt.
		for _, now := range []int64{received + window, received + 2*window} {
			if !authTimestampValid(ts, now, window) || !r.seen("key", now, window) {
				t.Fatalf("replay accepted at %d with window %d", now, window)
			}
		}
		if authTimestampValid(ts, received+2*window+1, window) {
			t.Fatal("expired timestamp accepted")
		}
		r.seen("new", received+2*window+1, window)
		if _, ok := r.items["key"]; ok {
			t.Fatal("expired cache entry retained")
		}
	}
}

func TestReplayWithoutTimeValidationAndClockRollback(t *testing.T) {
	r := replay{items: make(map[string]int64)}
	if r.seen("key", 1000, 0) {
		t.Fatal("first request rejected")
	}
	for _, now := range []int64{900, 1600} {
		if !r.seen("key", now, 0) {
			t.Fatal("replay accepted")
		}
	}
	if r.seen("key", 1601, 0) {
		t.Fatal("disabled time check cache should expire after 600 seconds")
	}
}

func TestReplayConcurrentRequests(t *testing.T) {
	r := replay{items: make(map[string]int64)}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !r.seen("same", 1000, 300) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d identical requests", accepted.Load())
	}
}

func TestReplayCleanupInterval(t *testing.T) {
	r := replay{items: make(map[string]int64)}
	r.seen("first", 1000, 300)
	r.seen("later", 1100, 300)
	r.seen("boundary", 1600, 300)
	if r.lastClean != 1000 {
		t.Fatal("cleanup ran before exceeding TTL")
	}
	r.seen("sweep", 1601, 300)
	if r.lastClean != 1601 {
		t.Fatal("cleanup did not run after TTL")
	}
	if _, ok := r.items["first"]; ok {
		t.Fatal("cleanup retained expired key")
	}
	if _, ok := r.items["later"]; !ok {
		t.Fatal("cleanup removed live key")
	}
	// Expired entries remain until the next sweep, but must not reject a key.
	r.seen("other", 1701, 300)
	if _, ok := r.items["later"]; !ok {
		t.Fatal("cleanup ran within TTL")
	}
	if r.seen("later", 1701, 300) {
		t.Fatal("expired key rejected between sweeps")
	}
	if !r.seen("later", 1701, 300) {
		t.Fatal("refreshed key was not protected")
	}
	r.seen("rollback", 900, 300)
	if r.lastClean != 1601 {
		t.Fatal("clock rollback triggered cleanup")
	}
}
