package client

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleUpdateRequest(t *testing.T) {
	old := RemoteUpdate
	t.Cleanup(func() { RemoteUpdate = old })

	RemoteUpdate = nil
	handleUpdateRequest() // disabled: ignored

	var runs atomic.Int32
	release := make(chan struct{})
	RemoteUpdate = func() error {
		runs.Add(1)
		<-release
		return nil
	}
	handleUpdateRequest()
	for runs.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	handleUpdateRequest() // ignored while the first one runs
	close(release)
	for updating.Load() {
		time.Sleep(time.Millisecond)
	}
	if n := runs.Load(); n != 1 {
		t.Fatalf("%d updates ran, want 1", n)
	}

	handleUpdateRequest() // a finished update allows the next
	for updating.Load() {
		time.Sleep(time.Millisecond)
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("%d updates ran after the first finished, want 2", n)
	}
}
