package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	lo, hi := 5*time.Second, 5*time.Minute
	prev := time.Duration(0)
	for n := 0; n < 20; n++ {
		base := lo
		for i := 0; i < n && base < hi; i++ {
			base *= 2
		}
		base = min(base, hi)
		for range 50 {
			d := retryDelay(n, lo, hi)
			if d > base || d < base-base/10 {
				t.Fatalf("n=%d: %v outside [%v, %v]", n, d, base-base/10, base)
			}
			if d > hi {
				t.Fatalf("n=%d: %v over the cap", n, d)
			}
		}
		if base < prev {
			t.Fatalf("n=%d: shrank", n)
		}
		prev = base
	}
	if prev != hi {
		t.Fatalf("never reached the cap: %v", prev)
	}
}

// A failed load is retried with a delay until it succeeds; the bundle is given once.
func TestShapingLoopRetriesUntilLoaded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var calls []time.Time
	var set [][]byte
	got := make(chan struct{})
	l := shapingLoop{
		load: func(context.Context) ([]byte, time.Time, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, time.Now())
			if len(calls) <= 3 {
				return nil, time.Time{}, errors.New("unreachable")
			}
			return []byte("b"), time.Time{}, nil
		},
		set: func(b []byte) {
			mu.Lock()
			set = append(set, b)
			mu.Unlock()
			close(got)
		},
		refresh:  time.Hour,
		retryMin: 4 * time.Millisecond,
		retryMax: 20 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() { l.run(ctx); close(done) }()
	select {
	case <-got:
	case <-ctx.Done():
		t.Fatal("never loaded")
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 4 || len(set) != 1 || string(set[0]) != "b" {
		t.Fatalf("calls=%d set=%q", len(calls), set)
	}
}

func TestShapingLoopKeepsOldAndRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	n := 0
	var times []time.Time
	var set []string
	third := make(chan struct{})
	l := shapingLoop{
		load: func(context.Context) ([]byte, time.Time, error) {
			mu.Lock()
			defer mu.Unlock()
			n++
			times = append(times, time.Now())
			switch n {
			case 1:
				return []byte("v1"), time.Time{}, nil
			case 2, 3:
				return nil, time.Time{}, errors.New("down")
			case 4:
				return []byte("v1"), time.Time{}, nil // unchanged: not set again
			case 5:
				close(third)
			}
			return []byte("v2"), time.Time{}, nil
		},
		set: func(b []byte) {
			mu.Lock()
			set = append(set, string(b))
			mu.Unlock()
		},
		refresh:  30 * time.Millisecond,
		retryMin: 2 * time.Millisecond,
		retryMax: 5 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() { l.run(ctx); close(done) }()
	select {
	case <-third:
	case <-ctx.Done():
		t.Fatal("no progress")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(set) != 2 || set[0] != "v1" || set[1] != "v2" {
		t.Fatalf("set %q", set)
	}
	// Failures retry within the cap; success is back on the refresh interval.
	if d := times[2].Sub(times[1]); d > 25*time.Millisecond {
		t.Fatalf("retry after %v", d)
	}
	if d := times[3].Sub(times[2]); d > 25*time.Millisecond {
		t.Fatalf("retry after %v", d)
	}
	if d := times[4].Sub(times[3]); d < 25*time.Millisecond {
		t.Fatalf("after success only %v", d)
	}
}

func TestShapingLoopStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := shapingLoop{
		load:     func(context.Context) ([]byte, time.Time, error) { return nil, time.Time{}, errors.New("down") },
		set:      func([]byte) { t.Error("set") },
		refresh:  time.Hour,
		retryMin: time.Hour,
		retryMax: time.Hour,
	}
	done := make(chan struct{})
	go func() { l.run(ctx); close(done) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
}
