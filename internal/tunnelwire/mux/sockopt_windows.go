//go:build windows

package mux

import (
	"net"
	"syscall"
)

func sysGetSock(syscall.RawConn) (bufferSize int, err error) {
	// https://github.com/golang/sys/blob/master/windows/syscall_windows.go#L1184
	// not support, WTF???
	// Todo
	// return syscall.GetsockoptInt((syscall.Handle)(unsafe.Pointer(fd.Fd())), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	bufferSize = 15 * 1024 * 1024
	return
}

func getConnFd(net.Conn) (syscall.RawConn, error) {
	return nil, nil
}
