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
	for _, c := range list {
		c.Id = int(s.TaskIncreaseId) + 1
		s.TaskIncreaseId = int32(c.Id)
		c.Flow.SetOwner(tunnelOwnerArgs(c)...)
		c.TargetType = model.NormalizeTargetType(c.TargetType)
		c.CompileDestACL()
		c.CompileSourceLists(acl.ScopeUser, taskOwner(c))
		s.Tasks.Store(c.Id, c)
	}
	return len(list) > 0
}
