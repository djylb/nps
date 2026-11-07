package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/event"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRetriesKeepIDAndSignature(t *testing.T) {
	var calls atomic.Int32
	ids := make(chan string, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get(HeaderSignature) != Sign("s3", r.Header.Get(HeaderTimestamp), body) {
			t.Error("bad signature")
		}
		var e event.Event
		if err := json.Unmarshal(body, &e); err != nil || e.ID != r.Header.Get(HeaderEventID) || e.Type != "tunnel_created" {
			t.Errorf("bad body %s: %v", body, err)
		}
		ids <- e.ID
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	b := event.NewBus()
	defer b.Close()
	s := Start(Config{URL: srv.URL, Secret: "s3", Retries: 2, Backoff: time.Millisecond}, b.Subscribe(4))
	defer s.Close()
	b.Publish("tunnel_created", map[string]int{"id": 1})
	waitFor(t, func() bool { return s.Delivered() == 1 })
	if a, c := <-ids, <-ids; a != c {
		t.Fatalf("id changed across retries: %s, %s", a, c)
	}
}

func TestGivesUpAfterRetriesAndOnRefusal(t *testing.T) {
	for _, tc := range []struct {
		status int
		calls  int32
	}{{http.StatusInternalServerError, 2}, {http.StatusUnauthorized, 1}, {http.StatusFound, 2}} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(tc.status)
		}))
		b := event.NewBus()
		s := Start(Config{URL: srv.URL, Retries: 1, Backoff: time.Millisecond}, b.Subscribe(1))
		b.Publish("x", nil)
		waitFor(t, func() bool { return s.Failed() == 1 })
		if calls.Load() != tc.calls {
			t.Errorf("status %d: %d calls, want %d", tc.status, calls.Load(), tc.calls)
		}
		s.Close()
		b.Close()
		srv.Close()
	}
}

func TestCloseInterruptsAHungReceiver(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	}))
	defer srv.Close()
	defer close(release)
	b := event.NewBus()
	defer b.Close()
	sub := b.Subscribe(1)
	s := Start(Config{URL: srv.URL, Timeout: time.Hour}, sub)
	b.Publish("first", nil)
	<-entered
	b.Publish("queued", nil)
	b.Publish("dropped", nil) // the publisher is never held up by the receiver
	if sub.Dropped() != 1 {
		t.Fatalf("dropped %d", sub.Dropped())
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hangs")
	}
}
