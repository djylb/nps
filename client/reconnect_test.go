package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/timeshim"
)

func fixedReconnect() (*Reconnect, *timeshim.Fake) {
	c := timeshim.NewFake(time.Unix(1000, 0))
	return &Reconnect{clock: c, jitter: func() float64 { return 0.5 }}, c
}

func TestReconnectBackoff(t *testing.T) {
	r, c := fixedReconnect()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for _, w := range want {
		if d := r.Delay(); d != w {
			t.Fatalf("delay %v, want %v", d, w)
		}
	}
	for range 20 {
		r.Delay()
	}
	if d := r.Delay(); d != reconnectMax {
		t.Fatalf("delay %v, want the cap %v", d, reconnectMax)
	}

	// A session that ends quickly is a failure; the drain of one does not
	// skip the wait either.
	r.up()
	c.Advance(reconnectStable - time.Second)
	r.ended(fmt.Errorf("%w: x", coreapi.ErrDrained))
	if d := r.Delay(); d != reconnectMax {
		t.Fatalf("delay after a short session %v, want %v", d, reconnectMax)
	}

	// A stable session resets the backoff.
	r.up()
	c.Advance(reconnectStable)
	r.ended(errors.New("lost"))
	if d := r.Delay(); d != time.Second {
		t.Fatalf("delay after a stable session %v, want 1s", d)
	}

	// The drain of a stable session is redialed within the drain window,
	// fully jittered (0.5 of it here).
	r.up()
	c.Advance(reconnectStable)
	r.ended(fmt.Errorf("%w: x", coreapi.ErrDrained))
	if d := r.Delay(); d != reconnectDrainWindow/2 {
		t.Fatalf("delay after a drain %v, want %v", d, reconnectDrainWindow/2)
	}
	if d := r.Delay(); d != time.Second {
		t.Fatalf("delay after the drain's redial failed %v, want 1s", d)
	}
}

func TestReconnectJitter(t *testing.T) {
	r, _ := fixedReconnect()
	r.jitter = func() float64 { return 0 }
	if d := r.Delay(); d != 800*time.Millisecond {
		t.Fatalf("delay %v, want 800ms", d)
	}
}

func TestReconnectWaitEndsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if NewReconnect().Wait(ctx) {
		t.Fatal("Wait went on after the context ended")
	}
}

// TestReconnectDrainFullJitter checks the bounds of the wait after a drain:
// [0, reconnectDrainWindow).
func TestReconnectDrainFullJitter(t *testing.T) {
	for _, j := range []float64{0, 0.25, 0.999999} {
		r, c := fixedReconnect()
		r.jitter = func() float64 { return j }
		r.up()
		c.Advance(reconnectStable)
		r.ended(coreapi.ErrDrained)
		d := r.Delay()
		if d < 0 || d >= reconnectDrainWindow || d != time.Duration(j*float64(reconnectDrainWindow)) {
			t.Fatalf("jitter %v: delay %v, want %v in [0, %v)", j, d, time.Duration(j*float64(reconnectDrainWindow)), reconnectDrainWindow)
		}
	}
}
