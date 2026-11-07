//go:build !windows

package mux

import (
	"net"
	"syscall"
)

func sysGetSock(rc syscall.RawConn) (bufferSize int, err error) {
	if rc == nil {
		return 5 * 1024 * 1024, nil
	}
	// Control reads the option on the connection's own socket: a duplicate
	// from File would switch the socket to blocking mode, after which
	// closing the connection waits for a pending read to return.
	if cerr := rc.Control(func(fd uintptr) {
		bufferSize, err = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}); cerr != nil {
		return 0, cerr
	}
	return bufferSize, err
}

func getConnFd(c net.Conn) (syscall.RawConn, error) {
	switch c := c.(type) {
	case *net.TCPConn:
		return c.SyscallConn()
	case *net.UDPConn:
		return c.SyscallConn()
	default:
		return nil, nil
	}
}
