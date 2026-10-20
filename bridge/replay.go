package bridge

import "sync"

type replay struct {
	mu        sync.Mutex
	items     map[string]int64
	lastClean int64
}

var rep = replay{items: make(map[string]int64, 100)}

func authTimestampValid(ts, now, window int64) bool {
	if window == 0 {
		return true
	}
	// Unsigned subtraction avoids overflow for untrusted timestamp extremes.
	if ts >= now {
		return uint64(ts)-uint64(now) <= uint64(window)
	}
	return uint64(now)-uint64(ts) <= uint64(window)
}

func (r *replay) seen(key string, now, window int64) bool {
	// Disabling timestamp validation keeps a bounded, ten-minute replay cache.
	if window == 0 {
		window = 300
	}
	ttl := uint64(window) * 2
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep the inclusive boundary and retain entries if the clock moves back.
	expired := func(ts int64) bool {
		return now >= ts && uint64(now)-uint64(ts) > ttl
	}
	if len(r.items) == 0 {
		r.lastClean = now
	} else if expired(r.lastClean) {
		for k, ts := range r.items {
			if expired(ts) {
				delete(r.items, k)
			}
		}
		r.lastClean = now
	}
	// A key can expire between sweeps; check it independently before rejecting.
	if ts, ok := r.items[key]; ok && !expired(ts) {
		return true
	}
	r.items[key] = now
	return false
}
