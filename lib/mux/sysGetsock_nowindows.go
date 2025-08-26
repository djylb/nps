//go:build !windows
// +build !windows

package mux

import (
	"net"
	"os"
	"syscall"
)

func sysGetSock(fd *os.File) (bufferSize int, err error) {
	if fd != nil {
		return syscall.GetsockoptInt(int(fd.Fd()), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	} else {
		return 5 * 1024 * 1024, nil
	}
}

func getConnFd(c net.Conn) (fd *os.File, err error) {
	switch c.(type) {
	case *net.TCPConn:
		fd, err = c.(*net.TCPConn).File()
		if err != nil {
			return
		}
		return
	case *net.UDPConn:
		fd, err = c.(*net.UDPConn).File()
		if err != nil {
			return
		}
		return
	default:
		return
	}
}
