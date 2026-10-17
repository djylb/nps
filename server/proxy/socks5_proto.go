package proxy

import (
	"net"
	"net/netip"

	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
)

func socks5ConnectReplyAddr(task *file.Tunnel, clientConn net.Conn) socks5.Addr {
	return socks5ReplyAddrFromNetAddr(task, addrOf(clientConn), nil, taskPort(task))
}

func socks5UDPReplyAddr(task *file.Tunnel, tcpConn net.Conn, udpConn net.PacketConn, clientIP net.IP) socks5.Addr {
	var tcpLocal net.Addr
	if tcpConn != nil {
		tcpLocal = tcpConn.LocalAddr()
	}
	var udpLocal net.Addr
	if udpConn != nil {
		udpLocal = udpConn.LocalAddr()
	}
	fallbackPort := 0
	if task != nil {
		fallbackPort = task.Port
	}
	return socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, fallbackPort, clientIP)
}

func socks5ReplyAddrFromNetAddr(task *file.Tunnel, tcpLocal, udpLocal net.Addr, fallbackPort int, clientIP ...net.IP) socks5.Addr {
	var client net.IP
	if len(clientIP) > 0 {
		client = common.NormalizeIP(clientIP[0])
	}
	wantV6 := client != nil && client.To4() == nil

	ip := socks5ReplyIP(task, tcpLocal, udpLocal, client, wantV6)
	port := socks5ReplyPort(udpLocal, tcpLocal, fallbackPort)
	if port <= 0 {
		port = fallbackPort
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return socks5ZeroReplyAddr(wantV6, port)
	}
	return socks5.Addr{IP: addr.Unmap(), Port: uint16(port)}
}

func addrOf(c net.Conn) net.Addr {
	if c == nil {
		return nil
	}
	return c.LocalAddr()
}

func taskPort(task *file.Tunnel) int {
	if task == nil {
		return 0
	}
	return task.Port
}

// socks5ReplyIP picks the address to advertise in a reply. An explicitly
// configured ServerIp always wins. A local address is only advertised when the
// client can reach it: a private local address is withheld from a public client
// (for example behind 1:1 NAT), and the zero address tells the client to reuse
// the server address of its control connection instead.
func socks5ReplyIP(task *file.Tunnel, tcpLocal, udpLocal net.Addr, client net.IP, wantV6 bool) net.IP {
	publicClient := client != nil && common.IsPublicIP(client)
	trySpecificIP := func(value net.IP) net.IP {
		value = common.NormalizeIP(value)
		if value == nil || value.IsUnspecified() || common.IsZeroIP(value) {
			return nil
		}
		if (value.To4() == nil) != wantV6 {
			return nil
		}
		return value
	}

	if task != nil {
		if ip := net.ParseIP(task.ServerIp); ip != nil {
			if specific := trySpecificIP(ip); specific != nil {
				return specific
			}
		}
	}
	for _, addr := range []net.Addr{udpLocal, tcpLocal} {
		var local net.IP
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if typed != nil {
				local = typed.IP
			}
		case *net.TCPAddr:
			if typed != nil {
				local = typed.IP
			}
		}
		specific := trySpecificIP(local)
		if specific == nil {
			continue
		}
		if publicClient && !common.IsPublicIP(specific) {
			continue
		}
		return specific
	}
	return nil
}

func socks5ReplyPort(primary, secondary net.Addr, fallback int) int {
	for _, addr := range []net.Addr{primary, secondary} {
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		case *net.TCPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		}
	}
	return fallback
}

func socks5ZeroReplyAddr(wantV6 bool, port int) socks5.Addr {
	if wantV6 {
		return socks5.Addr{IP: netip.IPv6Unspecified(), Port: uint16(port)}
	}
	return socks5.Addr{IP: netip.IPv4Unspecified(), Port: uint16(port)}
}
