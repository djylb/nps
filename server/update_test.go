package server

import (
	"errors"
	"testing"
)

func TestUpdateSelfDisabled(t *testing.T) {
	old := DisableRemoteUpdate
	t.Cleanup(func() { DisableRemoteUpdate = old })
	DisableRemoteUpdate = true
	if CanUpdateSelf() {
		t.Fatal("CanUpdateSelf with remote update disabled")
	}
	if v, err := UpdateSelf(); v != "" || !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("UpdateSelf = %q, %v; want ErrUpdateDisabled", v, err)
	}
	if updating.Load() {
		t.Fatal("a refused update left the update marked as running")
	}
}
