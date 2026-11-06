package common

import (
	"testing"
	"time"
)

func TestSyncTimeFailurePreservesOffset(t *testing.T) {
	timeMutex.Lock()
	oldOffset, oldServer, oldInterval, oldLast := timeOffset, ntpServer, syncInterval, lastSyncMono
	timeOffset = 10 * time.Minute
	ntpServer = "127.0.0.1:invalid-port"
	syncInterval = 5 * time.Minute
	lastSyncMono = time.Time{}
	timeMutex.Unlock()
	t.Cleanup(func() {
		timeMutex.Lock()
		timeOffset, ntpServer, syncInterval, lastSyncMono = oldOffset, oldServer, oldInterval, oldLast
		timeMutex.Unlock()
	})

	SyncTime()
	timeMutex.RLock()
	offset, last := timeOffset, lastSyncMono
	timeMutex.RUnlock()
	if offset != 10*time.Minute {
		t.Fatalf("failed sync reset offset to %v", offset)
	}
	if last.IsZero() {
		t.Fatal("failed sync must still throttle retries")
	}
}
