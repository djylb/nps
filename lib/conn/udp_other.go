//go:build !windows

package conn

import (
	"net"
	"strconv"

	"github.com/djylb/nps/lib/netaddr"
)

func NewUdpConnByAddr(addr string) (net.PacketConn, error) {
	udpAddr, network, bindAddr, exact, err := resolveUDPBindTarget(addr)
	if err != nil {
		return nil, err
	}
	if exact {
		return net.ListenPacket(network, bindAddr)
	}
	port := strconv.Itoa(netaddr.Port(addr))

	var conns []net.PacketConn
	if pc4, e4 := net.ListenPacket("udp4", ":"+port); e4 == nil {
		conns = append(conns, pc4)
	}
	if pc6, e6 := net.ListenPacket("udp6", ":"+port); e6 == nil {
		conns = append(conns, pc6)
	}
	if len(conns) == 1 {
		return conns[0], nil
	}
	if len(conns) > 1 {
		return NewSmartUdpConn(conns, udpAddr), nil
	}
	return net.ListenPacket("udp", addr)
}
