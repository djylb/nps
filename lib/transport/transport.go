//go:build !windows
// +build !windows

package transport

import (
	"net"

	"golang.org/x/sys/unix"
)

func SetTcpKeepAliveParams(tc *net.TCPConn, idle, intvl, probes int) error {
	raw, err := tc.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		if sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, TCP_KEEPIDLE, idle); sockErr != nil {
			return
		}
		if sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPINTVL, intvl); sockErr != nil {
			return
		}
		sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPCNT, probes)
	})
	if err != nil {
		return err
	}
	return sockErr
}
