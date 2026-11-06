//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestReloadReachesWatcher(t *testing.T) {
	done := make(chan struct{}, 1)
	WatchReload(func() { done <- struct{}{} })
	dir := t.TempDir()
	pid := strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "nps.pid"), []byte(pid), 0o600); err != nil {
		t.Fatal(err)
	}
	reload("nps", dir)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reload signal was not received")
	}
}
