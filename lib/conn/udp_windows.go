//go:build windows
// +build windows

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

	if ip4, e := netaddr.LocalUDP4IP(); e == nil && ip4 != nil && !ip4.IsUnspecified() {
		if pc4, e4 := net.ListenPacket("udp4", net.JoinHostPort(ip4.String(), port)); e4 == nil {
			conns = append(conns, pc4)
		}
	}

	if ip6, e := netaddr.LocalUDP6IP(); e == nil && ip6 != nil && !ip6.IsUnspecified() {
		if pc6, e6 := net.ListenPacket("udp6", net.JoinHostPort(ip6.String(), port)); e6 == nil {
			conns = append(conns, pc6)
		}
	}

	if len(conns) == 0 {
		return net.ListenPacket("udp", addr)
	}

	if len(conns) == 1 {
		return conns[0], nil
	}

	return NewSmartUdpConn(conns, udpAddr), nil
}
