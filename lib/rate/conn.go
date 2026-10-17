package rate

import (
	"io"
	"net"

	"github.com/djylb/byterate"
)

// NewRateConn limits conn's reads and writes together by r. A net.Conn stays
// a net.Conn. With a nil r, conn is returned as it is. Close wakes a Read or
// Write waiting for the limit.
func NewRateConn(conn io.ReadWriteCloser, r *Rate) io.ReadWriteCloser {
	if r == nil {
		return conn
	}
	if c, ok := conn.(net.Conn); ok {
		return byterate.NewConn(c, &r.l)
	}
	return byterate.NewReadWriteCloser(conn, &r.l)
}
