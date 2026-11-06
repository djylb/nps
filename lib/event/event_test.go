package event

import (
	"sync"
	"testing"
)

func TestSlowSubscriberDropsWithoutBlocking(t *testing.T) {
	b := NewBus()
	defer b.Close()
	slow, other := b.Subscribe(1), b.Subscribe(3)
	for range 3 {
		b.Publish("x", nil) // would hang here if a full queue blocked
	}
	if got := slow.Dropped(); got != 2 {
		t.Fatalf("slow dropped %d, want 2", got)
	}
	if got := other.Dropped(); got != 0 {
		t.Fatalf("other dropped %d", got)
	}
	first, second := <-other.C(), <-other.C()
	if first.ID == "" || first.ID == second.ID {
		t.Fatalf("ids %q, %q are not unique", first.ID, second.ID)
	}
}

func TestCloseKeepsQueuedAndIgnoresLaterPublish(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(2)
	b.Publish("kept", nil)
	b.Close()
	b.Close()
	b.Publish("late", nil)
	s.Close()
	if e, ok := <-s.C(); !ok || e.Type != "kept" {
		t.Fatalf("queued event lost: %v %v", e, ok)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("channel not closed")
	}
	if _, ok := <-b.Subscribe(1).C(); ok {
		t.Fatal("subscription of a closed bus is open")
	}
	var none *Bus
	none.Publish("x", nil)
	none.Close()
}

func TestPublishRacesClose(t *testing.T) {
	b := NewBus()
	b.Subscribe(1)
	s := b.Subscribe(1)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				b.Publish("x", nil)
			}
		})
	}
	wg.Go(s.Close)
	wg.Go(b.Close)
	wg.Wait()
}
