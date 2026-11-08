package slogx

import (
	"errors"
	"sync"
	"time"
)

// Sampling keeps a burst of lines and then a fraction: per key and Tick,
// the first First lines pass, then every Thereafter-th (zero: none). The
// next line that passes carries suppressed=<count>. Zero Tick is
// LimitWindow. Def.Limit is the special case First 1, Thereafter 0.
type Sampling struct {
	First      int
	Thereafter int
	Tick       time.Duration
}

// LimitWindow is the tick of Def.Limit.
const LimitWindow = time.Minute

func (s *Sampling) validate() error {
	if s.First < 0 || s.Thereafter < 0 || s.Tick < 0 {
		return errors.New("slogx: sampling with a negative field")
	}
	return nil
}

func (s Sampling) tick() time.Duration {
	if s.Tick <= 0 {
		return LimitWindow
	}
	return s.Tick
}

// MaxSampleKeys bounds the keys a sampler tracks. Beyond it, lines of new
// keys share one overflow key, so a flood of distinct keys (such as source
// addresses an attacker picks) is limited as one.
const MaxSampleKeys = 8192

const overflowKey = "\x00overflow"

// sampler counts per key.
type sampler struct {
	mu        sync.Mutex
	seen      map[string]*sampleSlot
	lastSweep time.Time
}

type sampleSlot struct {
	start      time.Time
	tick       time.Duration
	n          int
	suppressed uint64
}

// allow reports whether the line of key passes s at now, and how many
// lines of key were suppressed before it.
func (sm *sampler) allow(key string, s Sampling, now time.Time) (uint64, bool) {
	tick := s.tick()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.seen == nil {
		sm.seen = map[string]*sampleSlot{}
	}
	sl, ok := sm.seen[key]
	if !ok {
		if len(sm.seen) >= MaxSampleKeys {
			sm.sweep(now)
		}
		if len(sm.seen) >= MaxSampleKeys {
			key = overflowKey
			sl = sm.seen[key]
		}
		if sl == nil {
			sl = &sampleSlot{start: now, tick: tick}
			sm.seen[key] = sl
		}
	}
	if now.Sub(sl.start) >= sl.tick {
		sl.start, sl.n, sl.tick = now, 0, tick
	}
	sl.n++
	pass := sl.n <= s.First || (s.Thereafter > 0 && (sl.n-s.First)%s.Thereafter == 0)
	if !pass {
		sl.suppressed++
		return 0, false
	}
	n := sl.suppressed
	sl.suppressed = 0
	return n, true
}

// sweep drops the slots whose tick has passed, at most once per second so
// a full table costs one scan per second, not one per line. A dropped
// slot's suppressed count is lost; it was never followed by a line.
func (sm *sampler) sweep(now time.Time) {
	if now.Sub(sm.lastSweep) < time.Second {
		return
	}
	sm.lastSweep = now
	for k, v := range sm.seen {
		if now.Sub(v.start) >= v.tick {
			delete(sm.seen, k)
		}
	}
}

// sampleKey is the key of r: the event and the value of its Limit field,
// or for plain lines the module, level and message.
func sampleKey(r *Record, field string) string {
	if r.Def != nil {
		k := r.Event
		if field != "" && field != "*" {
			if v, ok := r.Attr(field); ok {
				k += "\x00" + v.String()
			}
		}
		return k
	}
	return r.Module + "\x00" + r.Level.String() + "\x00" + r.Msg
}
