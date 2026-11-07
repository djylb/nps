package bridge

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/djylb/guard/replay"
	"github.com/djylb/nps/lib/clock"
)

func TestAuthReplayTimeWindow(t *testing.T) {
	var r authReplay
	now := clock.Now().Unix()
	for _, tc := range []struct {
		ts, window int64
		valid      bool
	}{
		{now - 290, 300, true}, {now + 290, 300, true},
		{now - 310, 300, false}, {now + 310, 300, false},
		{0, 0, true}, {math.MaxInt64, 0, true},
		{math.MinInt64, 300, false}, {math.MaxInt64, 300, false},
		{0, -1, false}, {now, -1, true},
	} {
		if err := r.checkTime(tc.ts, tc.window); (err == nil) != tc.valid {
			t.Fatalf("timestamp=%d window=%d: %v", tc.ts, tc.window, err)
		}
	}
}

func TestAuthReplayRejectsReplays(t *testing.T) {
	var r authReplay
	now := clock.Now().Unix()
	if err := r.check(now, []byte("mac"), 300); err != nil {
		t.Fatal(err)
	}
	if err := r.check(now, []byte("mac"), 300); !errors.Is(err, replay.ErrReplay) {
		t.Fatalf("replay: %v", err)
	}
	// A handshake seen before stays rejected when the window changes or
	// the time check is turned off.
	for _, window := range []int64{30, 0} {
		if err := r.check(now, []byte("mac"), window); !errors.Is(err, replay.ErrReplay) {
			t.Fatalf("replay after the window changed to %d: %v", window, err)
		}
	}
	if err := r.check(0, []byte("other"), 0); err != nil {
		t.Fatalf("time check off: %v", err)
	}
	if err := r.check(1, []byte("other"), 0); !errors.Is(err, replay.ErrReplay) {
		t.Fatalf("replay with the time check off: %v", err)
	}
}

func TestAuthReplayConcurrentRequests(t *testing.T) {
	var r authReplay
	now := clock.Now().Unix()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if r.check(now, []byte("same"), 300) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d identical requests", accepted.Load())
	}
}
