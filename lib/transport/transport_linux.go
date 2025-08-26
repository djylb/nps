package transport

import (
	"fmt"
	"net"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const SO_ORIGINAL_DST = 80
const TCP_KEEPIDLE = unix.TCP_KEEPIDLE

func GetAddress(conn net.Conn) (string, error) {
	sysconn, ok := conn.(syscall.Conn)
	if !ok {
		return "", fmt.Errorf("connection does not support SyscallConn")
	}
	raw, err := sysconn.SyscallConn()
	if err != nil {
		return "", err
	}

	var dst string
	var opErr error

	err = raw.Control(func(fd uintptr) {
		// IPv4
		var sa4 unix.RawSockaddrInet4
		sz4 := uint32(unsafe.Sizeof(sa4))
		_, _, errno4 := unix.Syscall6(
			unix.SYS_GETSOCKOPT,
			fd,
			uintptr(unix.SOL_IP),
			uintptr(SO_ORIGINAL_DST),
			uintptr(unsafe.Pointer(&sa4)),
			uintptr(unsafe.Pointer(&sz4)),
			0,
		)
		if errno4 == 0 {
			ip := net.IPv4(sa4.Addr[0], sa4.Addr[1], sa4.Addr[2], sa4.Addr[3])
			port := int(sa4.Port>>8)&0xff | int(sa4.Port&0xff)<<8
			dst = ip.String() + ":" + strconv.Itoa(port)
			return
		}

		// IPv6
		var sa6 unix.RawSockaddrInet6
		sz6 := uint32(unsafe.Sizeof(sa6))
		_, _, errno6 := unix.Syscall6(
			unix.SYS_GETSOCKOPT,
			fd,
			uintptr(unix.SOL_IPV6),
			uintptr(SO_ORIGINAL_DST),
			uintptr(unsafe.Pointer(&sa6)),
			uintptr(unsafe.Pointer(&sz6)),
			0,
		)
		if errno6 == 0 {
			ip := net.IP(sa6.Addr[:])
			port := int(sa6.Port>>8)&0xff | int(sa6.Port&0xff)<<8
			dst = "[" + ip.String() + "]:" + strconv.Itoa(port)
			return
		}

		opErr = fmt.Errorf("not a redirected connection (errno4=%v, errno6=%v)", errno4, errno6)
	})

	if err != nil {
		return "", err
	}
	if opErr != nil {
		return "", opErr
	}
	return dst, nil
}
