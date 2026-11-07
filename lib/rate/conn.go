package rate

import (
	"io"
	"net"

	"github.com/djylb/byterate"
)

// NewRateConn limits conn's reads and writes together by r and by the global
// limit and optional per-entry directional limits, and counts global traffic;
// conn is a connection to an
// npc. A net.Conn stays a net.Conn. A nil r leaves only the global limit.
// Close wakes a Read or Write waiting for a limit.
func NewRateConn(conn io.ReadWriteCloser, r *Rate, entry ...*Directional) io.ReadWriteCloser {
	group := connectionGroup(r, entry)
	limits := byterate.WithLimiters(group.Reads(), group.Writes())

	if c, ok := conn.(net.Conn); ok {
		return byterate.NewConn(c, limits)
	}
	return byterate.NewStream(conn, limits)
}

// connectionGroup is the Group that limits one standalone wrapper: r's, under
// the global limit, with each non-nil entry's directional limits on top.
func connectionGroup(r *Rate, entry []*Directional) *byterate.Group {
	group := r.Group()
	for _, e := range entry {
		if e != nil {
			group = e.connectionGroup(group)
		}
	}
	return group
}

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
