package file

import (
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/lib/event"
)

// The events of the stored entries, published by DbUtils where entries change,
// and of client sessions, published by the bridge. Everything that creates,
// changes or removes an entry passes here, whether from the web, from a client
// configuration or from cleanup.
//
// A tunnel that restarts, as saving it from the web does, reports its stop and
// start as well as the update.
const (
	EventClientOnline  = "client.online"
	EventClientOffline = "client.offline"
	EventTunnelCreated = "tunnel.created"
	EventTunnelUpdated = "tunnel.updated"
	EventTunnelStarted = "tunnel.started"
	EventTunnelStopped = "tunnel.stopped"
	EventTunnelDeleted = "tunnel.deleted"
	EventHostCreated   = "host.created"
	EventHostUpdated   = "host.updated"
	EventHostStarted   = "host.started"
	EventHostStopped   = "host.stopped"
	EventHostDeleted   = "host.deleted"
)

// ClientInfo describes a client in events. Like TunnelInfo and HostInfo it
// holds no keys, passwords or certificates.
type ClientInfo struct {
	ID      int    `json:"id"`
	Remark  string `json:"remark,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Version string `json:"version,omitempty"`
}

// TunnelInfo describes a tunnel in events.
type TunnelInfo struct {
	ID       int    `json:"id"`
	ClientID int    `json:"client_id"`
	Mode     string `json:"mode"`
	ServerIP string `json:"server_ip,omitempty"`
	Port     int    `json:"port"`
	Target   string `json:"target,omitempty"`
	Remark   string `json:"remark,omitempty"`
	Enabled  bool   `json:"enabled"`
	// FromClient marks an entry delivered by the client's configuration, which
	// goes when the client does.
	FromClient bool `json:"from_client"`
}

// HostInfo describes a domain entry in events.
type HostInfo struct {
	ID         int    `json:"id"`
	ClientID   int    `json:"client_id"`
	Host       string `json:"host"`
	Location   string `json:"location,omitempty"`
	Scheme     string `json:"scheme,omitempty"`
	Target     string `json:"target,omitempty"`
	Remark     string `json:"remark,omitempty"`
	Enabled    bool   `json:"enabled"`
	FromClient bool   `json:"from_client"`
}

// NewTunnelInfo snapshots t.
func NewTunnelInfo(t *Tunnel) TunnelInfo {
	info := TunnelInfo{ID: t.Id, Mode: t.Mode, ServerIP: t.ServerIp, Port: t.Port, Remark: t.Remark, Enabled: t.Status, FromClient: t.NoStore}
	if t.Client != nil {
		info.ClientID = t.Client.Id
	}
	if t.Target != nil {
		info.Target = t.Target.TargetStr
	}
	return info
}

// NewHostInfo snapshots h.
func NewHostInfo(h *Host) HostInfo {
	info := HostInfo{ID: h.Id, Host: h.Host, Location: h.Location, Scheme: h.Scheme, Remark: h.Remark, Enabled: !h.IsClose, FromClient: h.NoStore}
	if h.Client != nil {
		info.ClientID = h.Client.Id
	}
	if h.Target != nil {
		info.Target = h.Target.TargetStr
	}
	return info
}

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
		t := v.(*Tunnel)
		s.events.tunnels.Store(t.Id, t.Status)
		return true
	})
	s.JsonDb.Hosts.Range(func(_, v any) bool {
		h := v.(*Host)
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

func (s *DbUtils) tunnelCreated(t *Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.tunnels.Store(t.Id, t.Status)
		bus.Publish(EventTunnelCreated, NewTunnelInfo(t))
	}
}

func (s *DbUtils) tunnelSaved(t *Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		bus.Publish(changeType(&s.events.tunnels, t.Id, t.Status, EventTunnelUpdated, EventTunnelStarted, EventTunnelStopped), NewTunnelInfo(t))
	}
}

func (s *DbUtils) tunnelDeleted(t *Tunnel) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.tunnels.Delete(t.Id)
		bus.Publish(EventTunnelDeleted, NewTunnelInfo(t))
	}
}

func (s *DbUtils) hostCreated(h *Host) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.hosts.Store(h.Id, !h.IsClose)
		bus.Publish(EventHostCreated, NewHostInfo(h))
	}
}

// TunnelChanged reports that the entry t was changed in place.
func (s *DbUtils) TunnelChanged(t *Tunnel) {
	notifyEntriesChanged()
	s.tunnelSaved(t)
}

// HostChanged reports that the entry h was changed in place.
func (s *DbUtils) HostChanged(h *Host) {
	notifyEntriesChanged()
	if bus := s.events.bus.Load(); bus != nil {
		bus.Publish(changeType(&s.events.hosts, h.Id, !h.IsClose, EventHostUpdated, EventHostStarted, EventHostStopped), NewHostInfo(h))
	}
}

func (s *DbUtils) hostDeleted(h *Host) {
	if bus := s.events.bus.Load(); bus != nil {
		s.events.hosts.Delete(h.Id)
		bus.Publish(EventHostDeleted, NewHostInfo(h))
	}
}

// ClientPresence reports that client id got or lost its session; c is nil if
// the client was deleted already. Sessions of the public and visitor keys, not
// being clients an administrator manages, are left out. A session joined by
// more connections reports online once.
func (s *DbUtils) ClientPresence(id int, c *Client, online bool) {
	bus := s.events.bus.Load()
	if bus == nil || id <= 0 || (c != nil && c.KeyKind != KeyControl) {
		return
	}
	if prev, loaded := s.events.online.Swap(id, online); loaded && prev.(bool) == online {
		return
	}
	info := ClientInfo{ID: id}
	if c != nil {
		info.Remark, info.Addr, info.Version = c.Remark, c.Addr, c.Version
	}
	if online {
		bus.Publish(EventClientOnline, info)
	} else {
		bus.Publish(EventClientOffline, info)
	}
}
