package transport

import (
	"fmt"
	"net"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const SoOriginalDst = 80
const TCPKeepIdle = unix.TCP_KEEPIDLE

// networkPort reads a sockaddr port, which the kernel stores in network byte
// order regardless of the host's endianness.
func networkPort(p *uint16) int {
	b := (*[2]byte)(unsafe.Pointer(p))
	return int(b[0])<<8 | int(b[1])
}

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
			uintptr(SoOriginalDst),
			uintptr(unsafe.Pointer(&sa4)),
			uintptr(unsafe.Pointer(&sz4)),
			0,
		)
		if errno4 == 0 {
			ip := net.IPv4(sa4.Addr[0], sa4.Addr[1], sa4.Addr[2], sa4.Addr[3])
			dst = net.JoinHostPort(ip.String(), strconv.Itoa(networkPort(&sa4.Port)))
			return
		}

		// IPv6
		var sa6 unix.RawSockaddrInet6
		sz6 := uint32(unsafe.Sizeof(sa6))
		_, _, errno6 := unix.Syscall6(
			unix.SYS_GETSOCKOPT,
			fd,
			uintptr(unix.SOL_IPV6),
			uintptr(SoOriginalDst),
			uintptr(unsafe.Pointer(&sa6)),
			uintptr(unsafe.Pointer(&sz6)),
			0,
		)
		if errno6 == 0 {
			ip := net.IP(sa6.Addr[:])
			dst = net.JoinHostPort(ip.String(), strconv.Itoa(networkPort(&sa6.Port)))
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
