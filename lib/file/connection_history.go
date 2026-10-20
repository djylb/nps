package file

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/djylb/nps/lib/servercfg"
)

// ConnectionEvent pairs one connection with its confirmed disconnection.
// OfflineTime remains empty while online or when shutdown prevented recording it.
type ConnectionEvent struct {
	OnlineTime  string
	OfflineTime string
}

// ConnectionHistory keeps a bounded, concurrency-safe persisted session history.
type ConnectionHistory struct {
	mu     sync.RWMutex
	events []ConnectionEvent
	online bool
}

func connectionHistoryLimit() int { return servercfg.Current().Runtime.ClientHistoryLimit }

func (h *ConnectionHistory) trimLocked(limit int) {
	if limit == 0 {
		h.events = nil
		return
	}
	if len(h.events) > limit {
		h.events = append([]ConnectionEvent(nil), h.events[len(h.events)-limit:]...)
	}
}

func (h *ConnectionHistory) record(online bool, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	limit := connectionHistoryLimit()
	h.trimLocked(limit)
	if limit == 0 {
		h.online = false
		return
	}
	if h.online == online {
		return
	}
	h.online = online
	if online {
		h.events = append(h.events, ConnectionEvent{OnlineTime: now.Format("2006-01-02 15:04:05")})
	} else if len(h.events) > 0 {
		h.events[len(h.events)-1].OfflineTime = now.Format("2006-01-02 15:04:05")
	}
	h.trimLocked(limit)
}

func (h *ConnectionHistory) MarshalJSON() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trimLocked(connectionHistoryLimit())
	return json.Marshal(h.events)
}

func (h *ConnectionHistory) UnmarshalJSON(b []byte) error {
	// Accept the initial event-only format deployed before paired sessions.
	var stored []struct {
		OnlineTime, OfflineTime, Time string
		Online                        bool
	}
	if err := json.Unmarshal(b, &stored); err != nil {
		return err
	}
	events := make([]ConnectionEvent, 0, len(stored))
	for _, e := range stored {
		if e.OnlineTime != "" {
			events = append(events, ConnectionEvent{e.OnlineTime, e.OfflineTime})
		} else if e.Time != "" {
			if e.Online {
				events = append(events, ConnectionEvent{OnlineTime: e.Time})
			} else if len(events) > 0 {
				events[len(events)-1].OfflineTime = e.Time
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = events
	h.trimLocked(connectionHistoryLimit())
	// Never fill an old session's missing offline time after a server restart.
	h.online = false
	return nil
}

func (s *Client) RecordConnection(online bool, now time.Time) {
	if s.Id <= 0 || s.ConnectionHistory == nil {
		return
	}
	s.ConnectionHistory.record(online, now)
}

// LastSeenTime snapshots the legacy last-online value without changing its meaning.
func (s *Client) LastSeenTime() string {
	s.RLock()
	defer s.RUnlock()
	return s.LastOnlineTime
}

// MarkSeen updates the legacy last-online value used by full and live lists.
func (s *Client) MarkSeen(now time.Time) {
	s.Lock()
	defer s.Unlock()
	s.LastOnlineTime = now.Format("2006-01-02 15:04:05")
}
