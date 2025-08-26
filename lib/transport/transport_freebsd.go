package transport

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sysPFINOUT     = 0x0
	sysPFIN        = 0x1
	sysPFOUT       = 0x2
	sysPFFWD       = 0x3
	sysDIOCNATLOOK = 0xc04c4417
	TCP_KEEPIDLE   = unix.TCP_KEEPIDLE
)

type pfiocNatlook struct {
	Saddr     [16]byte /* pf_addr */
	Daddr     [16]byte /* pf_addr */
	Rsaddr    [16]byte /* pf_addr */
	Rdaddr    [16]byte /* pf_addr */
	Sport     uint16
	Dport     uint16
	Rsport    uint16
	Rdport    uint16
	Af        uint8
	Proto     uint8
	Direction uint8
	Pad       [1]byte
}

const sizeofPfiocNatlook = 0x4c

func ioctl(s uintptr, ioc int, b []byte) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, s, uintptr(ioc), uintptr(unsafe.Pointer(&b[0]))); errno != 0 {
		return error(errno)
	}
	return nil
}

func GetAddress(conn net.Conn) (string, error) {
	f, err := os.Open("/dev/pf")
	if err != nil {
		return "", fmt.Errorf("failed to open /dev/pf: %v", err)
	}
	defer f.Close()

	fd := f.Fd()
	b := make([]byte, sizeofPfiocNatlook)
	nl := (*pfiocNatlook)(unsafe.Pointer(&b[0]))

	var raIP, laIP net.IP
	var raPort, laPort int
	switch ra := conn.RemoteAddr().(type) {
	case *net.TCPAddr:
		raIP = ra.IP
		raPort = ra.Port
		nl.Proto = syscall.IPPROTO_TCP
	case *net.UDPAddr:
		raIP = ra.IP
		raPort = ra.Port
		nl.Proto = syscall.IPPROTO_UDP
	}

	switch la := conn.LocalAddr().(type) {
	case *net.TCPAddr:
		laIP = la.IP
		laPort = la.Port
	case *net.UDPAddr:
		laIP = la.IP
		laPort = la.Port
	}

	if raIP.To4() != nil {
		if laIP.IsUnspecified() {
			laIP = net.ParseIP("127.0.0.1")
		}
		copy(nl.Saddr[:net.IPv4len], raIP.To4())
		copy(nl.Daddr[:net.IPv4len], laIP.To4())
		nl.Af = syscall.AF_INET
	}

	if raIP.To16() != nil && raIP.To4() == nil {
		if laIP.IsUnspecified() {
			laIP = net.ParseIP("::1")
		}
		copy(nl.Saddr[:], raIP)
		copy(nl.Daddr[:], laIP)
		nl.Af = syscall.AF_INET6
	}

	nl.Sport = uint16(raPort)
	nl.Dport = uint16(laPort)

	ioc := uintptr(sysDIOCNATLOOK)
	for _, dir := range []byte{sysPFOUT, sysPFIN} {
		nl.Direction = dir
		err = ioctl(fd, int(ioc), b)
		if err == nil || err != syscall.ENOENT {
			break
		}
	}

	if err != nil {
		return "", fmt.Errorf("ioctl failed: %v", err)
	}

	odPort := nl.Rdport
	var odIP net.IP
	switch nl.Af {
	case syscall.AF_INET:
		odIP = make(net.IP, net.IPv4len)
		copy(odIP, nl.Rdaddr[:net.IPv4len])
	case syscall.AF_INET6:
		odIP = make(net.IP, net.IPv6len)
		copy(odIP, nl.Rdaddr[:])
	}

	return fmt.Sprintf("%s:%d", odIP.String(), odPort), nil
}
