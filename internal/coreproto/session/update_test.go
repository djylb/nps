package session

import (
	"context"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/wire"
)

// A client advertises the update capability only when its DialConfig says it
// can act on an update request.
func TestUpdateCapAdvertisedByConfig(t *testing.T) {
	for _, can := range []bool{false, true} {
		h, err := wire.ParseHello(clientHello("t", "", "", can, false))
		if err != nil {
			t.Fatal(err)
		}
		if got := h.HasCap(wire.CapUpdate); got != can {
			t.Fatalf("canUpdate=%v: hello has update = %v", can, got)
		}
		if !h.HasCap(wire.CapLink) || !h.HasCap(wire.CapHealth) || !h.HasCap(wire.CapShaping) {
			t.Fatalf("canUpdate=%v: other capabilities lost: %v", can, h.Caps)
		}
	}
	if len(clientCaps) != 3 {
		t.Fatalf("clientCaps was changed by an advertisement: %v", clientCaps)
	}
}

func TestServerSeesUpdateCap(t *testing.T) {
	e := start(t, "tcp", rec(1, "abc", true))
	started := make(chan struct{}, 1)
	d := NewDialer(DialConfig{Candidates: e.cands(e.addr), VKey: "abc", Agent: "t", Timeout: 3 * time.Second,
		CanUpdate: true, Update: func() bool { started <- struct{}{}; return true }})
	e.dial(d)
	s := e.waitOnline()
	if !s.CanUpdate() {
		t.Fatal("server does not see the update capability")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.RequestUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("update not started")
	}
}

func TestServerSeesNoUpdateCap(t *testing.T) {
	e := start(t, "tcp", rec(1, "abc", true))
	e.dial(e.dialer("abc"))
	if s := e.waitOnline(); s.CanUpdate() {
		t.Fatal("server sees the update capability of a client that did not advertise it")
	}
}
