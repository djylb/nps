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

func getConnFd(net.Conn) (fd *os.File, err error) {
	return nil, nil
}
