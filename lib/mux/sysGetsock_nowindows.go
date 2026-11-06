//go:build !windows

package mux

import (
	"net"
	"os"
	"syscall"
)

func sysGetSock(fd *os.File) (bufferSize int, err error) {
	if fd != nil {
		return syscall.GetsockoptInt(int(fd.Fd()), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}
	return 5 * 1024 * 1024, nil
}

func getConnFd(c net.Conn) (fd *os.File, err error) {
	switch c := c.(type) {
	case *net.TCPConn:
		return c.File()
	case *net.UDPConn:
		return c.File()
	default:
		return nil, nil
	}
}
