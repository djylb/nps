package bridge

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/djylb/guard/replay"
	"github.com/djylb/nps/lib/clock"
)

// defaultAuthWindow is the time window, in seconds, of a negative
// auth_time_window, and half the time a handshake is remembered when the
// time check is off.
const defaultAuthWindow = 300

// authReplay rejects handshakes whose timestamp lies outside the time
// window or whose MAC was seen before.
type authReplay struct {
	mu     sync.Mutex
	window int64
	w      *replay.Window
	// prev is the window before auth_time_window changed; it still
	// rejects the handshakes it saw until prevUntil.
	prev      *replay.Window
	prevUntil time.Time
}

var authReplays authReplay

// checkTime rejects a handshake whose timestamp ts, in Unix seconds, lies
// outside window seconds of now, before its MAC is verified. A window of 0
// turns the time check off.
func (r *authReplay) checkTime(ts, window int64) error {
	w, _, at, err := r.prepare(ts, window)
	if err != nil {
		return err
	}
	return w.CheckTime(at)
}

// check accepts the first handshake with mac whose timestamp passes
// checkTime. With the time check off, handshakes are remembered for twice
// the default window.
func (r *authReplay) check(ts int64, mac []byte, window int64) error {
	w, prev, at, err := r.prepare(ts, window)
	if err != nil {
		return err
	}
	// Both windows remember the handshake, so that it stays rejected
	// when the setting changes again.
	err = w.Check(at, mac)
	if prev != nil {
		if prevErr := prev.Check(at, mac); errors.Is(prevErr, replay.ErrReplay) {
			return prevErr
		}
	}
	return err
}

// prepare returns the replay windows for window seconds and the time to
// check the handshake at.
func (r *authReplay) prepare(ts, window int64) (w, prev *replay.Window, at time.Time, err error) {
	if window < 0 {
		window = defaultAuthWindow
	}
	// Keep window + skew within time.Duration.
	window = min(window, math.MaxInt64/2/int64(time.Second))
	if w, prev, err = r.windows(window); err != nil {
		return nil, nil, time.Time{}, err
	}
	at = time.Unix(ts, 0)
	if window == 0 {
		at = clock.Now()
	}
	return w, prev, at, nil
}

// windows returns the replay window for window seconds, created when the
// setting changed, and the previous one while it still matters.
func (r *authReplay) windows(window int64) (w, prev *replay.Window, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := clock.Now()
	if r.w == nil || r.window != window {
		conf := replay.Config{
			Window: time.Duration(window) * time.Second,
			Skew:   time.Duration(window) * time.Second,
			Now:    clock.Now,
		}
		if window == 0 {
			conf.Window, conf.Skew = 2*defaultAuthWindow*time.Second, 0
		}
		next, err := replay.New(conf)
		if err != nil {
			return nil, nil, err
		}
		if r.w != nil {
			r.prev, r.prevUntil = r.w, now.Add(2*time.Duration(max(r.window, defaultAuthWindow))*time.Second)
		}
		r.w, r.window = next, window
	}
	if r.prev != nil && !now.Before(r.prevUntil) {
		r.prev = nil
	}
	return r.w, r.prev, nil
}
