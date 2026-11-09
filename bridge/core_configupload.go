package bridge

import (
	"net"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
)

// The config upload of a core session is a 0.35 transition: npc sends the
// tunnels and domains of its npc.conf with the byte exchange of the old
// protocol's WorkConfig, on a stream of the session, so that a config file
// keeps working when only the binary is upgraded. npc status runs on the same
// exchange, and npc register on wire.OpRegister. They are removed in the next
// major version, with this file and those operations.

// coreConfigUpload admits an upload of the client cs is authenticated as,
// under the rules of WorkConfig: the key kind, ConfigConnAllow and the
// public key, whose upload creates a client with a new key for npc to
// redial with. It returns what serves the stream, or nil to refuse.
func (s *Bridge) coreConfigUpload(cs coreapi.Session) func(net.Conn) {
	id := cs.ClientID()
	client, err := s.store.GetClient(id)
	if err != nil || !allowsFlag(client.KeyKind, legacy.WorkConfig) {
		coreAuthFailed(coreapi.AuthFailure{Source: cs.Source(), Carrier: cs.Carrier(), Client: id,
			Reason: "forbidden", Flag: legacy.WorkConfig})
		return nil
	}
	isPub := s.store.IsPubClient(id)
	if !isPub && !client.ConfigConnAllow {
		return nil
	}
	uuid := coreNodeUUID(cs)
	caps := legacy.CapsOf(legacy.LatestIndex, agentVersion(cs.Agent()))
	return func(nc net.Conn) {
		c := conn.NewConn(nc)
		run, ok := s.openConfig(c, id, isPub, caps, uuid)
		if !ok {
			_ = c.Close()
			return
		}
		run()
	}
}

// coreRegister admits an npc register of the client cs is authenticated as,
// under the rule of WorkRegister, and registers the session's source address.
func (s *Bridge) coreRegister(cs coreapi.Session) func(net.Conn) {
	id := cs.ClientID()
	client, err := s.store.GetClient(id)
	if err != nil || !allowsFlag(client.KeyKind, legacy.WorkRegister) {
		coreAuthFailed(coreapi.AuthFailure{Source: cs.Source(), Carrier: cs.Carrier(), Client: id,
			Reason: "forbidden", Flag: legacy.WorkRegister})
		return nil
	}
	return func(nc net.Conn) { s.register(conn.NewConn(nc), id) }
}
