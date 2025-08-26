//go:build windows
// +build windows

package mux

import (
	"net"
	"os"
)

func sysGetSock(fd *os.File) (bufferSize int, err error) {
	// https://github.com/golang/sys/blob/master/windows/syscall_windows.go#L1184
	// not support, WTF???
	// Todo
	// return syscall.GetsockoptInt((syscall.Handle)(unsafe.Pointer(fd.Fd())), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	bufferSize = 15 * 1024 * 1024
	return
}

func getConnFd(c net.Conn) (fd *os.File, err error) {
	switch c.(type) {
	case *net.TCPConn:
		//fd, err = c.(*net.TCPConn).File()
		//if err != nil {
		//	return
		//}
		return
	case *net.UDPConn:
		//fd, err = c.(*net.UDPConn).File()
		//if err != nil {
		//	return
		//}
		return
	default:
		return
	}
}
