package model

import (
	"encoding/hex"
	"strings"

	"github.com/djylb/nps/internal/coreproto/coreapi"
)

// ServiceScheme starts the target of a virtual backend: the rest is the
// service id in hex (§8), which depends on nothing a rename changes.
const ServiceScheme = "service://"

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

// ModeService is the mode of a virtual backend.
const ModeService = "service"

// IsVirtualBackend reports whether t describes a virtual service of npc, which
// is never stored: the server only ever sees what a session reports.
func (t *Tunnel) IsVirtualBackend() bool { return t.Mode == ModeService }
