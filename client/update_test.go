package client

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleUpdateRequest(t *testing.T) {
	old, oldRestart := RemoteUpdate, canRestartSelf
	t.Cleanup(func() { RemoteUpdate, canRestartSelf = old, oldRestart })
	canRestartSelf = func() bool { return true }

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

func TestUpdateEnabled(t *testing.T) {
	old, oldRestart := RemoteUpdate, canRestartSelf
	t.Cleanup(func() { RemoteUpdate, canRestartSelf = old, oldRestart })
	run := func() error { return nil }
	for _, c := range []struct {
		name    string
		update  func() error
		restart bool
		want    bool
	}{
		{"disabled", nil, true, false},
		{"cannot restart", run, false, false},
		{"disabled and cannot restart", nil, false, false},
		{"normal", run, true, true},
	} {
		RemoteUpdate, canRestartSelf = c.update, func() bool { return c.restart }
		if got := UpdateEnabled(); got != c.want {
			t.Errorf("%s: UpdateEnabled = %v, want %v", c.name, got, c.want)
		}
		if !c.want && AcceptUpdate() {
			t.Errorf("%s: update accepted", c.name)
		}
	}
}
