package file

import (
	"slices"
	"sync"

	"github.com/djylb/nps/lib/model"
)

// clientEntries maps each client to the ids of its tunnels or of its hosts. It
// is derived from the stored entries: every path that stores, changes in
// place or deletes an entry updates it after the change, so it never holds an
// owner that the entry itself does not name, except for the moment between
// the change and the update. Readers still check the owner on the entry.
//
// Lock order: mu, then the read lock of one entry, to read its owner. No code
// takes mu while holding an entry lock, and nothing else is locked under mu.
type clientEntries struct {
	owner    map[int]int              // entry id -> client id
	byClient map[int]map[int]struct{} // client id -> entry ids
	mu       sync.RWMutex
}

// sync makes the index of entry id match its stored state: owner returns the
// client of the entry as it is stored now, 0 if it is gone or has none. owner
// runs under mu, so that of two racing updates the later one reads the state
// both left behind and the index never keeps a stale owner.
func (x *clientEntries) sync(id int, owner func() int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	client := owner()
	old, ok := x.owner[id]
	if ok && old == client {
		return
	}
	if ok {
		x.dropLocked(id, old)
	}
	if client == 0 {
		return
	}
	if x.owner == nil {
		x.owner = make(map[int]int)
		x.byClient = make(map[int]map[int]struct{})
	}
	x.owner[id] = client
	ids := x.byClient[client]
	if ids == nil {
		ids = make(map[int]struct{})
		x.byClient[client] = ids
	}
	ids[id] = struct{}{}
}

func (x *clientEntries) dropLocked(id, client int) {
	delete(x.owner, id)
	if ids := x.byClient[client]; ids != nil {
		delete(ids, id)
		if len(ids) == 0 {
			delete(x.byClient, client)
		}
	}
}

func (x *clientEntries) reset() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.owner, x.byClient = nil, nil
}

// ids returns the entry ids of client in ascending order.
func (x *clientEntries) ids(client int) []int {
	x.mu.RLock()
	out := make([]int, 0, len(x.byClient[client]))
	for id := range x.byClient[client] {
		out = append(out, id)
	}
	x.mu.RUnlock()
	slices.Sort(out)
	return out
}

func tunnelClientID(t *model.Tunnel) int {
	t.RLock()
	defer t.RUnlock()
	if t.Client == nil {
		return 0
	}
	return t.Client.Id
}

func hostClientID(h *model.Host) int {
	h.RLock()
	defer h.RUnlock()
	if h.Client == nil {
		return 0
	}
	return h.Client.Id
}

// indexTunnel updates the index after tunnel id was stored, changed or deleted.
func (s *JsonDb) indexTunnel(id int) {
	s.tunnelClients.sync(id, func() int {
		if v, ok := s.Tasks.Load(id); ok {
			return tunnelClientID(v.(*model.Tunnel))
		}
		return 0
	})
}

// indexHost updates the index after host id was stored, changed or deleted.
func (s *JsonDb) indexHost(id int) {
	s.hostClients.sync(id, func() int {
		if v, ok := s.Hosts.Load(id); ok {
			return hostClientID(v.(*model.Host))
		}
		return 0
	})
}

// RangeClientTunnels calls fn, until it returns false, for each stored tunnel
// that client id owns, in id order.
func (s *DbUtils) RangeClientTunnels(id int, fn func(*model.Tunnel) bool) {
	for _, tid := range s.JsonDb.tunnelClients.ids(id) {
		v, ok := s.JsonDb.Tasks.Load(tid)
		if !ok {
			continue
		}
		if t := v.(*model.Tunnel); tunnelClientID(t) == id && !fn(t) {
			return
		}
	}
}

// RangeClientHosts calls fn, until it returns false, for each stored host
// that client id owns, in id order.
func (s *DbUtils) RangeClientHosts(id int, fn func(*model.Host) bool) {
	for _, hid := range s.JsonDb.hostClients.ids(id) {
		v, ok := s.JsonDb.Hosts.Load(hid)
		if !ok {
			continue
		}
		if h := v.(*model.Host); hostClientID(h) == id && !fn(h) {
			return
		}
	}
}
