package rate

import (
	"io"
	"net"

	"github.com/djylb/byterate"
)

// NewRateConn limits conn's reads and writes together by r and by the global
// limit, and counts them in the global traffic; conn is a connection to an
// npc. A net.Conn stays a net.Conn. A nil r leaves only the global limit.
// Close wakes a Read or Write waiting for a limit.
func NewRateConn(conn io.ReadWriteCloser, r *Rate) io.ReadWriteCloser {
	read, write := limiters(r)
	if c, ok := conn.(net.Conn); ok {
		return byterate.NewDuplexConn(c, read, write)
	}
	return byterate.NewDuplexReadWriteCloser(conn, read, write)
}
