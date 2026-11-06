// Package event is a small in-process hub. Publishing never blocks: each
// subscriber has a bounded queue and an event that does not fit is dropped for
// that subscriber and counted, so a slow subscriber costs the publisher nothing
// and the other subscribers nothing.
package event

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Event is one thing that happened. ID is unique across restarts, so a
// receiver can use it to ignore a delivery it has seen. Data must not change
// after publishing: it is read by subscribers later, on their goroutines.
type Event struct {
	ID   string    `json:"id"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Bus fans events out to its subscribers. A nil Bus is valid and drops
// everything.
type Bus struct {
	prefix string
	seq    atomic.Uint64
	mu     sync.RWMutex
	subs   map[*Subscription]struct{}
	closed bool
}

// NewBus returns an empty Bus.
func NewBus() *Bus {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Bus{prefix: hex.EncodeToString(b) + "-", subs: make(map[*Subscription]struct{})}
}

// Publish hands an event to every subscriber with room for it. After Close it
// does nothing.
func (b *Bus) Publish(eventType string, data any) {
	if b == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed || len(b.subs) == 0 {
		return
	}
	e := Event{ID: b.prefix + strconv.FormatUint(b.seq.Add(1), 10), Type: eventType, Time: time.Now(), Data: data}
	for s := range b.subs {
		select {
		case s.c <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// Subscribe returns a subscription with a queue of the given size, at least
// one. On a closed Bus its channel is already closed.
func (b *Bus) Subscribe(queue int) *Subscription {
	s := &Subscription{bus: b, c: make(chan Event, max(queue, 1))}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.c)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

// Close ends all subscriptions. Events still queued can be received, then the
// channels report closed. It is safe to call more than once.
func (b *Bus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		close(s.c)
		delete(b.subs, s)
	}
}

// Subscription is one subscriber's queue.
type Subscription struct {
	bus     *Bus
	c       chan Event
	dropped atomic.Uint64
}

// C returns the queue. It is closed when the subscription or the Bus closes.
func (s *Subscription) C() <-chan Event { return s.c }

// Dropped returns how many events did not fit in the queue.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close ends the subscription. It is safe to call more than once and after
// the Bus closed.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if _, ok := s.bus.subs[s]; ok {
		delete(s.bus.subs, s)
		close(s.c)
	}
}
