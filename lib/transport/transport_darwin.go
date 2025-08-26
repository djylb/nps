package transport

import (
	"fmt"
	"net"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/djylb/nps/lib/common"
	"golang.org/x/sys/unix"
)

const (
	PfOut        = 2
	LEN          = 4*16 + 4*4 + 4*1
	IOCInOut     = 0x80000000
	IOCPARM_MASK = 0x1FFF
	DIOCNATLOOK  = IOCInOut | ((LEN & IOCPARM_MASK) << 16) | ('D' << 8) | 23
	TCP_KEEPIDLE = unix.TCP_KEEPALIVE
)

func GetAddress(conn net.Conn) (string, error) {
	fd, err := syscall.Open("/dev/pf", syscall.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("failed to open /dev/pf: %v", err)
	}
	defer syscall.Close(fd)

	var nl struct {
		saddr, daddr, rsaddr, rdaddr       [16]byte
		sxport, dxport, rsxport, rdxport   [4]byte
		af, proto, protoVariant, direction uint8
	}

	nl.direction = PfOut

	var raIP, laIP net.IP
	var raPort, laPort int

	switch ra := conn.RemoteAddr().(type) {
	case *net.TCPAddr:
		raIP = ra.IP
		raPort = ra.Port
		nl.proto = syscall.IPPROTO_TCP
	case *net.UDPAddr:
		raIP = ra.IP
		raPort = ra.Port
		nl.proto = syscall.IPPROTO_UDP
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
		// IPv4
		nl.af = syscall.AF_INET
		if laIP.IsUnspecified() {
			laIP = net.ParseIP("127.0.0.1")
		}
		copy(nl.saddr[:net.IPv4len], raIP.To4())
		copy(nl.daddr[:net.IPv4len], laIP.To4())
	} else if raIP.To16() != nil {
		// IPv6
		nl.af = syscall.AF_INET6
		if laIP.IsUnspecified() {
			laIP = net.ParseIP("::1")
		}
		copy(nl.saddr[:], raIP)
		copy(nl.daddr[:], laIP)
	}

	nl.sxport[0], nl.sxport[1] = byte(raPort>>8), byte(raPort)
	nl.dxport[0], nl.dxport[1] = byte(laPort>>8), byte(laPort)

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), DIOCNATLOOK, uintptr(unsafe.Pointer(&nl)))
	if errno != 0 {
		return "", fmt.Errorf("failed to get redirected address: %v", errno)
	}

	odPort := nl.rdxport
	var odIP net.IP
	switch nl.af {
	case syscall.AF_INET:
		odIP = make(net.IP, net.IPv4len)
		copy(odIP, nl.rdaddr[:net.IPv4len])
	case syscall.AF_INET6:
		odIP = make(net.IP, net.IPv6len)
		copy(odIP, nl.rdaddr[:])
	}

	return common.BuildAddress(odIP.String(), strconv.Itoa(int(odPort[0]<<8|odPort[1]))), nil
}
