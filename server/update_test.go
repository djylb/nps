package server

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestUpdateSelfDisabled(t *testing.T) {
	s := New(file.New(t.TempDir()), Config{DisableRemoteUpdate: true})
	t.Cleanup(func() { _ = s.Close() })
	if s.CanUpdateSelf() {
		t.Fatal("CanUpdateSelf with remote update disabled")
	}
	if v, err := s.UpdateSelf(); v != "" || !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("UpdateSelf = %q, %v; want ErrUpdateDisabled", v, err)
	}
	if s.updating.Load() {
		t.Fatal("a refused update left the update marked as running")
	}
}
