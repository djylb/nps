package bridge

import (
	"sync"
	"time"
)

type replay struct {
	mu    sync.Mutex
	items map[string]int64
	ttl   int64
}

var rep = replay{
	items: make(map[string]int64, 100),
	ttl:   300,
}

func IsReplay(key string) bool {
	now := time.Now().Unix()
	rep.mu.Lock()
	defer rep.mu.Unlock()
	expireBefore := now - rep.ttl
	for k, ts := range rep.items {
		if ts < expireBefore {
			delete(rep.items, k)
		}
	}
	if _, ok := rep.items[key]; ok {
		return true
	}
	rep.items[key] = now
	return false
}
