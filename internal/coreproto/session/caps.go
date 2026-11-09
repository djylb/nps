package session

import (
	"slices"

	"github.com/djylb/nps/internal/coreproto/wire"
)

// serverCaps and clientCaps are what Hello advertises (§2.3).
var (
	serverCaps = []uint64{wire.CapLink}
	clientCaps = []uint64{wire.CapLink, wire.CapHealth, wire.CapShaping}
)

// compressAlgs is what Hello lists as decompressible (§7.2: must include
// flate).
var compressAlgs = []uint64{wire.CompressFlate, wire.CompressZstd, wire.CompressSnappy}

func serverHello() []byte {
	return wire.AppendHello(nil, &wire.Hello{Caps: serverCaps, CompressAlgs: compressAlgs})
}

// clientHello advertises update only when the client can act on an update
// request (§2.3, §6).
func clientHello(agent, label string, canUpdate bool) []byte {
	caps := clientCaps
	if canUpdate {
		caps = append(slices.Clone(caps), wire.CapUpdate)
	}
	return wire.AppendHello(nil, &wire.Hello{Caps: caps, CompressAlgs: compressAlgs, Agent: agent, Label: label})
}
