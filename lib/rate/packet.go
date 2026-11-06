package rate

import (
	"net"

	"github.com/djylb/byterate"
)

// NewVisitorPacketConn limits and counts the datagrams of pc, a socket that
// visitors send to, as NewRateConn does for a connection to an npc: by r, by
// the global limit and by the optional per-entry directional limits. The
// directions are those of the visitor, so datagrams read from pc are uploads
// and datagrams written to it downloads. Datagrams that the limits do not
// let through at once are dropped instead of waiting, as a full socket buffer
// drops them.
func NewVisitorPacketConn(pc net.PacketConn, r *Rate, entry ...*Directional) *byterate.PacketConn {
	group := connectionGroup(r, entry)
	return byterate.NewPacketConn(pc, byterate.WithLimiters(group.Writes(), group.Reads()), byterate.WithDrop())
}
