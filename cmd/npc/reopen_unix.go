//go:build !windows && !sdk

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/djylb/nps/lib/slogx"
)

// watchReopen reopens the log files on every SIGHUP, so that an external
// tool such as logrotate can move them away. It runs for the life of the
// process.
func watchReopen() {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGHUP)
	go func() {
		for range s {
			slogx.Default.Reopen()
		}
	}()
}
