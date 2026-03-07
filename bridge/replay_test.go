package bridge

import (
	"testing"
	"time"
)

func TestIsReplay(t *testing.T) {
	rep.mu.Lock()
	rep.items = map[string]int64{}
	rep.ttl = 300
	rep.mu.Unlock()

	if got := IsReplay("token-1"); got {
		t.Fatal("first key observation should not be replay")
	}
	if got := IsReplay("token-1"); !got {
		t.Fatal("second key observation should be replay")
	}
}

func TestIsReplayEvictsExpiredItems(t *testing.T) {
	now := time.Now().Unix()
	rep.mu.Lock()
	rep.items = map[string]int64{
		"expired": now - 10,
	}
	rep.ttl = 1
	rep.mu.Unlock()

	if got := IsReplay("new-key"); got {
		t.Fatal("new key should not be replay")
	}

	rep.mu.Lock()
	_, hasExpired := rep.items["expired"]
	rep.mu.Unlock()
	if hasExpired {
		t.Fatal("expired entry should be evicted on IsReplay call")
	}
}
