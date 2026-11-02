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
	group := r.Group()
	for _, e := range entry {
		if e != nil {
			group = e.connectionGroup(group)
		}
	}
	limits := byterate.WithLimiters(group.Reads(), group.Writes())

	if c, ok := conn.(net.Conn); ok {
		return byterate.NewConn(c, limits)
	}
	return byterate.NewStream(conn, limits)
}
