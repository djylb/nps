package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/timeshim"
)

// ReconnectFirst is the wait after the first failure in a row; each further
// failure doubles it up to reconnectMax.
var ReconnectFirst = time.Second

const (
	reconnectMax = 2 * time.Minute
	// reconnectStable is how long a session must stay up for the failures
	// before it to be forgotten; a shorter one counts as a failure, so a
	// server that admits and drops the client at once is not hammered.
	reconnectStable = 30 * time.Second
	// reconnectJitter spreads the clients a server restart dropped together.
	reconnectJitter = 0.2
	// reconnectDrainWindow is the full-jitter window of the reconnect after a
	// stable session the server drained: every client of a restarted server
	// comes back at once, spread over it rather than in one burst.
	reconnectDrainWindow = time.Second
)

// Reconnect paces the reconnects of one client profile: exponential backoff
// with jitter, reset by a session that stayed up, and a wait in
// [0, reconnectDrainWindow) after a stable session the server drained (its
// restart). It is the one pacing of every
// reconnect loop of the core protocol client; the session loop reports into
// the Reconnect its context carries (WithReconnect).
type Reconnect struct {
	clock  timeshim.Clock
	jitter func() float64 // in [0, 1)

	mu      sync.Mutex
	fails   int       // failures in a row
	upAt    time.Time // when the last session came up, zero without one
	stable  bool      // the last session stayed up for reconnectStable
	drained bool      // the server drained the last session
	failure string    // the reason of the last failed connect (connectFailure)
}

// NewReconnect returns the pacing of a profile that has not failed yet.
func NewReconnect() *Reconnect {
	return &Reconnect{clock: timeshim.Real, jitter: rand.Float64}
}

type reconnectKey struct{}

// WithReconnect returns ctx carrying r, which the sessions run under ctx
// report into.
func WithReconnect(ctx context.Context, r *Reconnect) context.Context {
	return context.WithValue(ctx, reconnectKey{}, r)
}

// reconnectOf returns the Reconnect of ctx, or a new one for a loop that
// paces itself.
func reconnectOf(ctx context.Context) *Reconnect {
	if r, ok := ctx.Value(reconnectKey{}).(*Reconnect); ok {
		return r
	}
	return NewReconnect()
}

// up records that a session came up.
func (r *Reconnect) up() {
	r.mu.Lock()
	r.upAt, r.failure = r.clock.Now(), ""
	r.mu.Unlock()
}

// failed records the reason of a connect that failed.
func (r *Reconnect) failed(reason string) {
	r.mu.Lock()
	r.failure = reason
	r.mu.Unlock()
}

// lastFailure is the reason of the last failed connect since a session came
// up, "" when none failed.
func (r *Reconnect) lastFailure() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failure
}

// ended records how the session that came up last ended.
func (r *Reconnect) ended(err error) {
	r.mu.Lock()
	r.stable = !r.upAt.IsZero() && timeshim.Since(r.clock, r.upAt) >= reconnectStable
	r.drained = errors.Is(err, coreapi.ErrDrained)
	r.mu.Unlock()
}

// Delay returns the wait before the next attempt and counts the run that just
// ended: a failure unless its session stayed up.
func (r *Reconnect) Delay() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	stable, drained := r.stable, r.drained
	r.upAt, r.stable, r.drained = time.Time{}, false, false
	if stable {
		r.fails = 0
		if drained {
			return time.Duration(r.jitter() * float64(reconnectDrainWindow))
		}
	}
	d := ReconnectFirst
	for i := 0; i < r.fails && d < reconnectMax; i++ {
		d *= 2
	}
	d = min(d, reconnectMax)
	r.fails++
	return d + time.Duration((r.jitter()*2-1)*reconnectJitter*float64(d))
}

// Wait waits Delay. It returns false if ctx ended first.
func (r *Reconnect) Wait(ctx context.Context) bool {
	return timeshim.Sleep(ctx, r.clock, r.Delay()) == nil
}
