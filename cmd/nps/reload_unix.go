//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchReload calls fn for every reload signal, one at a time.
func watchReload(fn func()) {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR1)
	go func() {
		for range s {
			fn()
		}
	}()
}
