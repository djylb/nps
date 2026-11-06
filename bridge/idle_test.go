package bridge

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleTimerClosesAfterIdleWithoutActivity(t *testing.T) {
	closed := make(chan struct{}, 1)
	timer := NewIdleTimer(20*time.Millisecond, func() {
		closed <- struct{}{}
	})
	defer timer.Stop()

	select {
	case <-closed:
		// expected
	case <-time.After(300 * time.Millisecond):
		t.Fatal("idle timer did not close after idle timeout")
	}
}

func TestIdleTimerIncDecDefersCloseUntilIdleAgain(t *testing.T) {
	closed := make(chan struct{}, 1)
	timer := NewIdleTimer(25*time.Millisecond, func() {
		closed <- struct{}{}
	})
	defer timer.Stop()

	timer.Inc()
	time.Sleep(60 * time.Millisecond)

	select {
	case <-closed:
		t.Fatal("idle timer closed while activity count was non-zero")
	default:
	}

	timer.Dec()
	select {
	case <-closed:
		// expected after Dec reset the timer
	case <-time.After(300 * time.Millisecond):
		t.Fatal("idle timer did not close after activity returned to zero")
	}
}

func TestIdleTimerStopPreventsClose(t *testing.T) {
	var called atomic.Int32
	timer := NewIdleTimer(20*time.Millisecond, func() {
		called.Add(1)
	})
	timer.Stop()

	time.Sleep(80 * time.Millisecond)

	if got := called.Load(); got != 0 {
		t.Fatalf("close callback invoked %d times after Stop, want 0", got)
	}
}

func TestIdleTimerIgnoresIncDecAfterClosed(t *testing.T) {
	var called atomic.Int32
	timer := NewIdleTimer(20*time.Millisecond, func() {
		called.Add(1)
	})

	timer.Stop()
	timer.Inc()
	timer.Dec()
	time.Sleep(80 * time.Millisecond)

	if got := called.Load(); got != 0 {
		t.Fatalf("close callback invoked %d times after Stop+Inc/Dec, want 0", got)
	}
}
