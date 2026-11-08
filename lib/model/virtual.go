package model

import (
	"encoding/hex"
	"strings"

	"github.com/djylb/nps/internal/coreproto/coreapi"
)

// ServiceScheme starts the target of a virtual backend: the rest is the
// service id in hex (§8), which depends on nothing a rename changes.
const ServiceScheme = "service://"

// FileServiceKind is the kind of a file service.
const FileServiceKind = "file"

// ServiceTarget is the target that reaches the service of kind and name.
func ServiceTarget(kind, name string) string {
	return ServiceScheme + hex.EncodeToString(coreapi.ServiceID(kind, name))
}

// ParseServiceTarget returns the service id of a target written by
// ServiceTarget; ok is false for any other target.
func ParseServiceTarget(target string) (id []byte, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(target), ServiceScheme)
	if !found {
		return nil, false
	}
	id, err := hex.DecodeString(rest)
	if err != nil || len(id) != coreapi.ServiceIDLen {
		return nil, false
	}
	return id, true
}

// IsVirtualBackend reports whether t is a virtual backend: served by npc,
// reached through the target of another tunnel, listening on nothing.
func (t *Tunnel) IsVirtualBackend() bool {
	if t.Mode != "file" || t.Target == nil {
		return false
	}
	_, ok := ParseServiceTarget(t.Target.TargetStr)
	return ok
}
