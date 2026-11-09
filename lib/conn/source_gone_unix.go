//go:build unix

package conn

import (
	"errors"
	"syscall"
)

func peekSocketClosed(rc syscall.RawConn) bool {
	closed := false
	err := rc.Control(func(fd uintptr) {
		var b [1]byte
		n, _, err := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		switch {
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
		case err != nil:
			closed = true
		default:
			closed = n == 0
		}
	})
	return closed || err != nil
}
