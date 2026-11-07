package conn

import (
	"context"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxyproto"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/rate"
)

// GetConn returns LinkConn of conn limited and counted by rt and entry, as
// rate.NewRateConn does.
func GetConn(conn net.Conn, cpt, snappy bool, rt *rate.Rate, isServer, isLocal bool, entry ...*rate.Directional) io.ReadWriteCloser {
	return rate.NewRateConn(LinkConn(conn, cpt, snappy, isServer, isLocal), rt, entry...)
}

// LinkConn wraps conn, a link to an npc, with its encryption or compression;
// a local link is used as it is.
func LinkConn(conn net.Conn, cpt, snappy bool, isServer, isLocal bool) io.ReadWriteCloser {
	switch {
	case isLocal:
		return conn
	case cpt && isServer:
		return crypt.NewTlsServerConn(conn)
	case cpt:
		return crypt.NewTlsClientConn(conn)
	case snappy:
		return NewSnappyConn(conn)
	default:
		return conn
	}
}

// HandleUdp5 relays the SOCKS5 UDP datagrams of serverConn, one per frame,
// through a local UDP socket bound to localIP, if it is an IP address: each
// datagram's payload goes to its destination, and each datagram received
// goes back with its source as the address. It returns, closing serverConn,
// when ctx ends, either side closes or fails, or no datagram passed in
// either direction for timeout.
func HandleUdp5(ctx context.Context, serverConn net.Conn, timeout time.Duration, localIP string) {
	local, err := net.ListenUDP("udp", netaddr.BuildUDPBindAddr(localIP))
	if err != nil {
		_ = serverConn.Close()
		logs.Error("bind local udp port error %v", err)
		return
	}
	relay := socks5.DatagramRelay{
		IdleTimeout: timeout,
		OnError:     func(err error) { logs.Debug("socks5 udp: %v", err) },
	}
	framed := netx.NewFramedConn(serverConn, netx.WithFramedDatagrams())
	if err := relay.Relay(ctx, framed, socks5.NewPacketConn(local)); err != nil {
		logs.Debug("socks5 udp relay ended: %v", err)
	}
}

// CopyWaitGroup conn1 mux conn
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, rt *rate.Rate,
	flows []Flow, isServer bool, proxyProtocol int, rb []byte, task Entry, isLocal, isFramed bool) {
	var limit *rate.Directional
	if isServer && task != nil {
		// Client-side relay wrappers stay out of server-side metering.
		limit = task.RateLimiter()
	}
	connHandle := GetConn(conn1, crypt, snappy, rt, isServer, isLocal, limit)
	if isFramed {
		// connHandle only wraps conn1, which every path below closes.
		//noinspection GoResourceLeak
		connHandle = netx.NewFramedConn(netx.WrapConn(connHandle, conn1, netx.WithWrapParentClose()), netx.WithFramedDatagrams())
	}
	if proxyHeader := ProxyProtocolHeader(proxyProtocol, conn2.RemoteAddr(), conn2.LocalAddr()); proxyHeader != nil {
		logs.Debug("Sending Proxy Protocol v%d header to backend: %v -> %v", proxyProtocol, proxyHeader.Source, proxyHeader.Destination)
		if _, err := proxyHeader.WriteTo(connHandle); err != nil {
			logs.Warn("failed to write proxy protocol header: %v", err)
			_ = conn1.Close()
			_ = conn2.Close()
			return
		}
	}
	if rb != nil {
		if _, err := connHandle.Write(rb); err != nil {
			logs.Warn("failed to write buffered pre-read data: %v", err)
			_ = conn1.Close()
			_ = conn2.Close()
			return
		}
	}
	remote := ""
	if task != nil {
		if ra := conn2.RemoteAddr(); ra != nil {
			remote = ra.String()
		}
	}
	relay(connHandle, conn2, flows, task, remote)
	_ = conn1.Close()
	_ = conn2.Close()
}

func ParseAddr(addr string) net.Addr {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return &net.TCPAddr{IP: net.ParseIP(addr), Port: 0}
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.IPv4zero
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 0
	}
	return &net.TCPAddr{IP: ip, Port: port}
}

// ProxyProtocolHeader returns the PROXY protocol header of version 1 or 2
// for a connection from client to target, or nil for any other version. The
// addresses are taken as proxyproto.NewHeader takes them: an unknown or
// unspecified target becomes the zero address of the client's family, and an
// IPv4 and an IPv6 address are both sent as IPv6. A pair that it rejects, such
// as a nil client, an address without an IP or addresses of other types,
// gives a header without addresses ("PROXY UNKNOWN" for version 1, a PROXY
// command of the AF_UNSPEC family for version 2), so that the backend uses
// the connection's own addresses.
func ProxyProtocolHeader(version int, client, target net.Addr) *proxyproto.Header {
	v := proxyproto.Version(version)
	if v != proxyproto.V1 && v != proxyproto.V2 {
		return nil
	}
	h, err := proxyproto.NewHeader(v, client, target)
	if err != nil {
		logs.Debug("PROXY protocol header without addresses for %v -> %v: %v", client, target, err)
		return &proxyproto.Header{Version: v}
	}
	return h
}
