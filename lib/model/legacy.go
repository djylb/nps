package model

import (
	"strconv"

	"github.com/djylb/nps/lib/netaddr"
)

// This file is the one place a file tunnel with a listening port, as earlier
// versions wrote it, becomes a virtual backend. Remove it, and its calls, in
// the next major version.

// ConvertFileTunnel makes the file tunnel t a virtual backend, in place, and
// returns one tcp tunnel per port of t that listens there and targets the
// backend; a tunnel with no port gets none. It reports false, changing
// nothing, for what is no file tunnel or is a backend already, so applying
// it twice is applying it once. The returned tunnels have no Id.
//
// The service is named by the first port, or by the remark without one, so
// that its identifier stays the same across restarts and renames.
func ConvertFileTunnel(t *Tunnel) ([]*Tunnel, bool) {
	if t == nil || t.Mode != "file" || t.IsVirtualBackend() {
		return nil, false
	}
	var ports []int
	if t.Port > 0 {
		ports = []int{t.Port}
	} else {
		ports = netaddr.GetPorts(t.Ports)
	}
	name := t.Remark
	if len(ports) > 0 {
		name = "port-" + strconv.Itoa(ports[0])
	}
	if name == "" {
		name = "default"
	}
	target := ServiceTarget(FileServiceKind, name)
	companions := make([]*Tunnel, 0, len(ports))
	for _, p := range ports {
		c := t.Clone()
		c.Id, c.Mode, c.Port, c.Ports = 0, "tcp", p, strconv.Itoa(p)
		c.LocalPath, c.StripPre, c.ReadOnly, c.TargetAddr = "", "", false, ""
		c.Target = &Target{TargetStr: target}
		c.Flow = new(Flow)
		c.BackendOf = t.Id // 0 for a tunnel of npc.conf, which has no id yet
		companions = append(companions, c)
	}
	t.ServiceName = name
	t.Port, t.Ports = 0, ""
	t.Target = &Target{TargetStr: target}
	return companions, true
}
