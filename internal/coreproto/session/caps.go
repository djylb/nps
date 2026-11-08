package session

import "github.com/djylb/nps/internal/coreproto/wire"

// serverCaps and clientCaps are what Hello advertises (§2.3).
var (
	serverCaps = []uint64{wire.CapLink}
	clientCaps = []uint64{wire.CapLink, wire.CapHealth, wire.CapUpdate, wire.CapShaping}
)

// compressAlgs is what Hello lists as decompressible (§7.2: must include
// flate).
var compressAlgs = []uint64{wire.CompressFlate, wire.CompressZstd, wire.CompressSnappy}

func serverHello() []byte {
	return wire.AppendHello(nil, &wire.Hello{Caps: serverCaps, CompressAlgs: compressAlgs})
}

func clientHello(agent, label string) []byte {
	return wire.AppendHello(nil, &wire.Hello{Caps: clientCaps, CompressAlgs: compressAlgs, Agent: agent, Label: label})
}
