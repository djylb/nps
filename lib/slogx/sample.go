package slogx

import (
	"log/slog"
	"sync"
	"time"
)

// Sampling keeps a burst of lines and then a fraction: per key and Tick,
// the first First lines pass, then every Thereafter-th (zero: none). The
// next line that passes carries suppressed=<count>. Def.Limit is the
// special case First 1, Thereafter 0, Tick LimitWindow.
type Sampling struct {
	First      int
	Thereafter int
	Tick       time.Duration
}

// LimitWindow is the tick of Def.Limit.
const LimitWindow = time.Minute

// sampler counts per key.
type sampler struct {
	mu   sync.Mutex
	seen map[string]*sampleSlot
}

type sampleSlot struct {
	start      time.Time
	n          int
	suppressed uint64
}

const maxSampleKeys = 8192

// allow reports whether the line of key passes s at now, and how many
// lines of key were suppressed before it.
func (sm *sampler) allow(key string, s Sampling, now time.Time) (uint64, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.seen == nil {
		sm.seen = map[string]*sampleSlot{}
	}
	sl, ok := sm.seen[key]
	if !ok {
		if len(sm.seen) >= maxSampleKeys {
			for k, v := range sm.seen {
				if now.Sub(v.start) >= s.Tick && v.suppressed == 0 {
					delete(sm.seen, k)
				}
			}
			if len(sm.seen) >= maxSampleKeys {
				// Every key is busy: let the line through rather than grow.
				return 0, true
			}
		}
		sl = &sampleSlot{start: now}
		sm.seen[key] = sl
	}
	if now.Sub(sl.start) >= s.Tick {
		sl.start, sl.n = now, 0
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

// sampleKey is the key of r under d: the event and the value of its Limit
// field, or for plain lines the module, level and message.
func sampleKey(r *Record, field string) string {
	if r.Event != "" {
		k := r.Event
		if field != "" && field != "*" {
			if v, ok := r.Attr(field); ok {
				k += "\x00" + v.String()
			}
		}
		return k
	}
	return r.Module + "\x00" + slog.Level(r.Level).String() + "\x00" + r.Msg
}
