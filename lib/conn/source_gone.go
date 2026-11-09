package conn

import (
	"net"
	"syscall"
)

// SourceGone reports whether the peer of c has closed it, looking at the
// socket under c without taking anything from it, so that it can be asked
// while the connection is still to be relayed. Data waiting to be read means
// the peer is there. It is false where the question cannot be answered: a
// connection without a socket, or a platform without peekSocketClosed.
func SourceGone(c net.Conn) bool {
	for c != nil {
		if sc, ok := c.(syscall.Conn); ok {
			rc, err := sc.SyscallConn()
			if err != nil {
				return true
			}
			return peekSocketClosed(rc)
		}
		u, ok := c.(interface{ NetConn() net.Conn })
		if !ok {
			return false
		}
		c = u.NetConn()
	}
	return false
}
