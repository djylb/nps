package file

import (
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/model"
)

// This file is the one place stored tunnels of earlier versions are
// converted. Remove it, and its calls, in the next major version.

// legacyTask converts the mode of a tunnel of an earlier version: socks5 and
// httpProxy are mixProxy, p2p is secret (point-to-point access) with the
// same password, so existing visitors keep working.
// It reports whether t was converted.
func legacyTask(t *model.Tunnel) bool {
	switch t.Mode {
	case "socks5":
		t.Mode = "mixProxy"
		t.HttpProxy = false
		t.Socks5Proxy = true
	case "httpProxy":
		t.Mode = "mixProxy"
		t.HttpProxy = true
		t.Socks5Proxy = false
	case "p2p":
		log.Warn("p2p tunnel converted to secret (point-to-point access)", "tunnel", t.Id)
		t.Mode = "secret"
	default:
		return false
	}
	return true
}

// addFileCompanions stores the tunnels of a converted file tunnel under new
// ids, once every stored tunnel is known, and reports whether there were any.
func (s *JsonDb) addFileCompanions(list []*model.Tunnel) bool {
	added := false
	for _, c := range list {
		if s.portTaken(c) {
			// The backend stays; a tunnel to it can be made by hand. The
			// conversion is done, so this is said only once.
			log.Warn("generated tunnel of a file backend not created", "port", c.Port, "reason", "port_in_use", "target", c.Target.TargetStr)
			continue
		}
		added = true
		c.Id = int(s.TaskIncreaseId) + 1
		s.TaskIncreaseId = int32(c.Id)
		c.Flow.SetOwner(tunnelOwnerArgs(c)...)
		c.TargetType = model.NormalizeTargetType(c.TargetType)
		c.CompileDestACL()
		c.CompileSourceLists(acl.ScopeUser, taskOwner(c))
		s.Tasks.Store(c.Id, c)
	}
	return added
}

// portTaken reports whether a stored tunnel already listens where t would:
// the same port, on a protocol both use (TCP, UDP) and on listen addresses
// that overlap, as the port check of a new tunnel does.
func (s *JsonDb) portTaken(t *model.Tunnel) (taken bool) {
	tcp, udp := t.ListenKinds()
	s.Tasks.Range(func(_, v any) bool {
		o := v.(*model.Tunnel)
		if o.Id == t.Id || o.Port == 0 || o.Port != t.Port || !model.ListenIPsOverlap(o.ServerIp, t.ServerIp) {
			return true
		}
		otcp, oudp := o.ListenKinds()
		taken = tcp && otcp || udp && oudp
		return !taken
	})
	return taken
}
