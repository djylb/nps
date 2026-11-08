package session

import "github.com/djylb/nps/internal/coreproto/wire"

// serverCaps and clientCaps are what Hello advertises: only what is
// implemented at this point of the move to the core protocol. link, health
// and update (§2.3) join these lists with the steps that bring them.
var (
	serverCaps []uint64
	clientCaps []uint64
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
