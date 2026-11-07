//go:build !windows

package main

import (
	"syscall"
	"testing"
	"time"
)

func TestReloadSignalReachesWatcher(t *testing.T) {
	done := make(chan struct{}, 1)
	watchReload(func() { done <- struct{}{} })
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reload signal was not received")
	}
}
