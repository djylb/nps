//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchReload calls fn for every reload signal, one at a time, until the
// returned stop is called.
func watchReload(fn func()) (stop func()) {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range s {
			fn()
		}
	}()
	return func() {
		signal.Stop(s)
		close(s)
		<-done
	}
}
