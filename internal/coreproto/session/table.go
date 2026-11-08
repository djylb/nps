package session

import (
	"sync"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

// Store is what the entry reads of the stored clients. The store stays the
// authority: the table below is an index of it.
type Store interface {
	// RangeClients calls fn for every client that can connect.
	RangeClients(fn func(coreapi.ClientRecord) bool)
}

// clientEntry is one row of the handle table. Immutable: a change replaces it.
type clientEntry struct {
	coreapi.ClientRecord
	handle [wire.HandleLen]byte
	psk    []byte
}

// handleTable maps a Handle to its client, kept current by the store's change
// notifications (Acceptor.ClientChanged).
type handleTable struct {
	mu       sync.RWMutex
	byHandle map[[wire.HandleLen]byte]*clientEntry
	byID     map[int]*clientEntry
}

func newHandleTable(store Store) *handleTable {
	t := &handleTable{byHandle: map[[wire.HandleLen]byte]*clientEntry{}, byID: map[int]*clientEntry{}}
	store.RangeClients(func(r coreapi.ClientRecord) bool {
		t.set(r)
		return true
	})
	return t
}

// set installs r and returns the row it replaced, nil if none.
func (t *handleTable) set(r coreapi.ClientRecord) (old *clientEntry) {
	var e *clientEntry
	t.mu.RLock()
	prev := t.byID[r.ID]
	t.mu.RUnlock()
	if prev != nil && prev.VKey == r.VKey {
		e = &clientEntry{ClientRecord: r, handle: prev.handle, psk: prev.psk}
	} else if r.VKey != "" {
		h, psk, err := wire.Derive(r.VKey)
		if err != nil {
			log.Warn("client key not usable", "client", r.ID, "err", err)
		} else {
			e = &clientEntry{ClientRecord: r, handle: [wire.HandleLen]byte(h), psk: psk}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old = t.byID[r.ID]
	if old != nil {
		delete(t.byHandle, old.handle)
		delete(t.byID, r.ID)
	}
	if e != nil {
		t.byHandle[e.handle] = e
		t.byID[r.ID] = e
	}
	return old
}

func (t *handleTable) remove(id int) (old *clientEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old = t.byID[id]
	if old != nil {
		delete(t.byHandle, old.handle)
		delete(t.byID, id)
	}
	return old
}

func (t *handleTable) lookup(handle []byte) *clientEntry {
	if len(handle) != wire.HandleLen {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byHandle[[wire.HandleLen]byte(handle)]
}

func (t *handleTable) get(id int) *clientEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byID[id]
}
