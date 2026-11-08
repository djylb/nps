package model

import (
	"strconv"

	"github.com/djylb/nps/lib/netaddr"
)

// This file is the one place a file tunnel with a listening port, as earlier
// versions wrote it, becomes a virtual backend. Remove it, and its calls, in
// the next major version.

// FileServiceKind is the kind npc reports its file services under.
const FileServiceKind = "file"

// ConvertFileTunnel makes the file tunnel t the description of a local virtual
// service, in place, and returns one tcp tunnel per port of t that listens
// there and targets the service; a tunnel with no port gets none. npc serves
// what t describes; the server keeps only the returned tunnels. It reports false, changing
// nothing, for what is no file tunnel or is a backend already, so applying
// it twice is applying it once. The returned tunnels have no Id.
//
// The service is named by the first port, or by the remark without one, so
// that its identifier stays the same across restarts and renames.
func ConvertFileTunnel(t *Tunnel) ([]*Tunnel, bool) {
	if t == nil || t.Mode != "file" {
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
		c.TargetType = TargetTCP
		c.Flow = new(Flow)
		c.Generated = true
		companions = append(companions, c)
	}
	t.Mode, t.ServiceKind, t.ServiceName = ModeService, FileServiceKind, name
	t.Port, t.Ports = 0, ""
	t.Target = &Target{TargetStr: target}
	t.TargetType = TargetTCP // a file service is tcp
	return companions, true
}
