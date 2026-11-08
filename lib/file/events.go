package file

import (
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/event"
	"github.com/djylb/nps/lib/logevent"
)

// The events of the stored entries, published by DbUtils where entries change,
// and of client sessions, published by the bridge. Everything that creates,
// changes or removes an entry passes here, whether from the web, from a client
// configuration or from cleanup.
//
// A tunnel that restarts, as saving it from the web does, reports its stop and
// start as well as the update.

// entryEvents tells what changed on entries, which are edited in place and so
// cannot be compared with their stored self.
type entryEvents struct {
	bus     atomic.Pointer[event.Bus]
	tunnels sync.Map // id -> whether enabled
	hosts   sync.Map // id -> whether enabled
	online  sync.Map // client id -> last published presence
}

// SetEventBus makes s publish to bus; nil stops it, and then changes cost
// nothing.
func (s *DbUtils) SetEventBus(bus *event.Bus) {
	s.events.bus.Store(nil)
	s.events.tunnels.Clear()
	s.events.hosts.Clear()
	s.events.online.Clear()
	if bus == nil {
		return
	}
	s.JsonDb.Tasks.Range(func(_, v any) bool {
		t := v.(*model.Tunnel)
		s.events.tunnels.Store(t.Id, t.Status)
		return true
	})
	s.JsonDb.Hosts.Range(func(_, v any) bool {
		h := v.(*model.Host)
		s.events.hosts.Store(h.Id, !h.IsClose)
		return true
	})
	s.events.bus.Store(bus)
}

// changeType picks the event of a saved entry from its enabled state.
func changeType(seen *sync.Map, id int, enabled bool, updated, started, stopped string) string {
	prev, loaded := seen.Swap(id, enabled)
	switch {
	case !loaded || prev.(bool) == enabled:
		return updated
	case enabled:
		return started
	default:
		return stopped
	}
}

// clientChanged publishes a change of a client an administrator manages.
func (s *DbUtils) clientChanged(name string, c *model.Client) {
	if f := s.clientHook.Load(); f != nil && c != nil {
		(*f)(c, name == logevent.ClientDeleted.Name)
	}
	if bus := s.events.bus.Load(); bus != nil && c != nil && c.Id > 0 && c.KeyKind == model.KeyControl {
		bus.Publish(name, model.ClientInfo{ID: c.Id, Remark: c.Remark, Addr: c.Addr, Version: c.Version})
	}
}

// OnClientChanged makes f called after a client was created, changed or
// deleted (deleted true). It runs on the changing goroutine and returns
// promptly.
func (s *DbUtils) OnClientChanged(f func(c *model.Client, deleted bool)) { s.clientHook.Store(&f) }

// ClientChanged reports that the client c was changed in place.
func (s *DbUtils) ClientChanged(c *model.Client) { s.clientChanged(logevent.ClientUpdated.Name, c) }

func (s *DbUtils) tunnelCreated(t *model.Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.tunnels.Store(t.Id, t.Status)
		bus.Publish(logevent.TunnelCreated.Name, model.NewTunnelInfo(t))
	}
}

func (s *DbUtils) tunnelSaved(t *model.Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		bus.Publish(changeType(&s.events.tunnels, t.Id, t.Status, logevent.TunnelUpdated.Name, logevent.TunnelStarted.Name, logevent.TunnelStopped.Name), model.NewTunnelInfo(t))
	}
}

func (s *DbUtils) tunnelDeleted(t *model.Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.tunnels.Delete(t.Id)
		bus.Publish(logevent.TunnelDeleted.Name, model.NewTunnelInfo(t))
	}
}

func (s *DbUtils) hostCreated(h *model.Host) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.hosts.Store(h.Id, !h.IsClose)
		bus.Publish(logevent.HostCreated.Name, model.NewHostInfo(h))
	}
}

// TunnelChanged reports that the entry t was changed in place.
func (s *DbUtils) TunnelChanged(t *model.Tunnel) {
	s.JsonDb.notifyEntriesChanged()
	s.tunnelSaved(t)
}

// HostChanged reports that the entry h was changed in place.
func (s *DbUtils) HostChanged(h *model.Host) {
	s.JsonDb.notifyEntriesChanged()
	if bus := s.events.bus.Load(); bus != nil {
		bus.Publish(changeType(&s.events.hosts, h.Id, !h.IsClose, logevent.HostUpdated.Name, logevent.HostStarted.Name, logevent.HostStopped.Name), model.NewHostInfo(h))
	}
}

func (s *DbUtils) hostDeleted(h *model.Host) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.hosts.Delete(h.Id)
		bus.Publish(logevent.HostDeleted.Name, model.NewHostInfo(h))
	}
}

// ClientPresence reports that client id got or lost its session; c is nil if
// the client was deleted already. Sessions of the public and visitor keys, not
// being clients an administrator manages, are left out. A session joined by
// more connections reports online once.
func (s *DbUtils) ClientPresence(id int, c *model.Client, online bool) {
	bus := s.events.bus.Load()
	if bus == nil || id <= 0 || (c != nil && c.KeyKind != model.KeyControl) {
		return
	}
	if prev, loaded := s.events.online.Swap(id, online); loaded && prev.(bool) == online {
		return
	}
	info := model.ClientInfo{ID: id}
	if c != nil {
		info.Remark, info.Addr, info.Version = c.Remark, c.Addr, c.Version
	}
	if online {
		bus.Publish(logevent.ClientOnline.Name, info)
	} else {
		bus.Publish(logevent.ClientOffline.Name, info)
	}
}
