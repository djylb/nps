//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchReload calls fn for every reload signal, one at a time.
func WatchReload(fn func()) {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR1)
	go func() {
		for range s {
			fn()
		}
	}()
}
