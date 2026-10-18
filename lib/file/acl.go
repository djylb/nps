package file

import (
	"context"
	"net"
	"slices"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/logs"
)

// SourceLists are the source access lists of a level, the global settings, a
// client, a tunnel or a host: which visitor addresses may connect. Every
// level has to allow an address.
type SourceLists struct {
	// IpListPriority decides an address on both lists or on neither when
	// both are set: acl.DenyFirst (0) or acl.AllowFirst (1).
	IpListPriority int
	BlackIpList    []string
	WhiteIpList    []string
	sourceAcl      atomic.Pointer[acl.Source]
	// RegisteredOnly additionally requires the visitor address to be
	// registered for the client, see package ipreg. The global settings do
	// not use it.
	RegisteredOnly bool
	compiled       atomic.Bool
}

// SetSourceLists replaces the lists and compiles them.
func (l *SourceLists) SetSourceLists(priority int, black, white []string, scope acl.Scope, owner string) {
	l.IpListPriority = acl.NormalizePriority(priority)
	l.BlackIpList = slices.Clone(black)
	l.WhiteIpList = slices.Clone(white)
	l.CompileSourceLists(scope, owner)
}

// CopySourceLists copies the lists of other and their compiled form.
func (l *SourceLists) CopySourceLists(other *SourceLists) {
	l.RegisteredOnly = other.RegisteredOnly
	l.IpListPriority = other.IpListPriority
	l.BlackIpList = slices.Clone(other.BlackIpList)
	l.WhiteIpList = slices.Clone(other.WhiteIpList)
	l.sourceAcl.Store(other.sourceAcl.Load())
	l.compiled.Store(other.compiled.Load())
}

// CompileSourceLists compiles the lists. Rules that cannot be used, such as
// geoip: rules without geodata, are logged and skipped.
func (l *SourceLists) CompileSourceLists(scope acl.Scope, owner string) {
	src, err := acl.CompileSource(scope, l.IpListPriority, l.BlackIpList, l.WhiteIpList)
	if err != nil {
		logs.Warn("Access lists of %s: rules skipped: %v", owner, err)
	}
	l.sourceAcl.Store(src)
	l.compiled.Store(true)
}

// SourceACL returns the compiled lists, compiling them as user lists if that
// has not happened yet.
func (l *SourceLists) SourceACL() *acl.Source {
	if l == nil {
		return nil
	}
	if !l.compiled.Load() {
		l.CompileSourceLists(acl.ScopeUser, "an entry")
	}
	return l.sourceAcl.Load()
}

// RefreshSourceLists recompiles lists whose geodata changed.
func (l *SourceLists) RefreshSourceLists(owner string) {
	if err := l.sourceAcl.Load().Refresh(); err != nil {
		logs.Warn("Access lists of %s: geodata update not applied to some rules: %v", owner, err)
	}
}

// GlobalDenied reports whether the global access lists, the firewall in
// front of every listener of nps, deny addr, and logs a denial. what names
// the listener in the log.
func (s *DbUtils) GlobalDenied(addr net.Addr, what string) bool {
	g := s.GetGlobal()
	if g == nil {
		return false
	}
	src := g.SourceACL()
	if src == nil || acl.Allows(addr, src, nil, nil) {
		return false
	}
	logs.Warn("%s access from %v %v", what, addr, acl.Decide(addr, []string{"global"}, src, nil, nil))
	return true
}

// GlobalDeniedString is GlobalDenied for an address given as text.
func (s *DbUtils) GlobalDeniedString(addr, what string) bool {
	g := s.GetGlobal()
	if g == nil {
		return false
	}
	src := g.SourceACL()
	if src == nil || acl.AllowsString(addr, src, nil, nil) {
		return false
	}
	logs.Warn("%s access from %s %v", what, addr, acl.DecideString(addr, []string{"global"}, src, nil, nil))
	return true
}

// RefreshAccessLists recompiles the access lists whose geodata changed.
func (s *DbUtils) RefreshAccessLists() {
	if g := s.GetGlobal(); g != nil {
		g.RefreshSourceLists("the global settings")
	}
	s.JsonDb.Clients.Range(func(_, v any) bool {
		c := v.(*Client)
		c.RefreshSourceLists(clientOwner(c))
		return true
	})
	s.JsonDb.Tasks.Range(func(_, v any) bool {
		t := v.(*Tunnel)
		t.RefreshSourceLists(taskOwner(t))
		t.RLock()
		d := t.destAcl
		t.RUnlock()
		if err := d.Refresh(); err != nil {
			logs.Warn("Destination list of tunnel %d: geodata update not applied to some rules: %v", t.Id, err)
		}
		return true
	})
	s.JsonDb.Hosts.Range(func(_, v any) bool {
		h := v.(*Host)
		h.RefreshSourceLists(hostOwner(h))
		return true
	})
}

// WatchAccessLists checks the geodata files every interval until ctx ends
// and refreshes the access lists when one of them changed, so that updated
// geodata takes effect. Checking reads file metadata only.
func WatchAccessLists(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	last := acl.GeoGeneration()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if gen := acl.GeoGeneration(); gen != last {
				last = gen
				GetDb().RefreshAccessLists()
			}
		}
	}
}
