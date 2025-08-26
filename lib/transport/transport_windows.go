package transport

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

const SIO_KEEPALIVE_VALS = 0x98000004

type tcpKeepalive struct {
	OnOff             uint32
	KeepAliveTime     uint32
	KeepAliveInterval uint32
}

func SetTcpKeepAliveParams(tc *net.TCPConn, idle, intvl, _ int) error {
	raw, err := tc.SyscallConn()
	if err != nil {
		return err
	}
	ka := tcpKeepalive{
		OnOff:             1,
		KeepAliveTime:     uint32(idle * 1000),  // 毫秒
		KeepAliveInterval: uint32(intvl * 1000), // 毫秒
	}
	var bytesReturned uint32
	var serr error
	err = raw.Control(func(fd uintptr) {
		serr = windows.WSAIoctl(windows.Handle(fd),
			SIO_KEEPALIVE_VALS,
			(*byte)(unsafe.Pointer(&ka)), uint32(unsafe.Sizeof(ka)),
			nil, 0,
			&bytesReturned,
			nil, 0)
	})
	if err != nil {
		return err
	}
	return serr
}
