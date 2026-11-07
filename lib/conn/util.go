package conn

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxyproto"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/rate"
)

var log = obs.Module("conn")

// GetConn returns LinkConn of conn limited and counted by rt and entry, as
// rate.NewRateConn does.
func GetConn(conn net.Conn, cpt, snappy bool, rt *rate.Rate, cert *tls.Certificate, isLocal bool, entry ...*rate.Directional) io.ReadWriteCloser {
	return rate.NewRateConn(LinkConn(conn, cpt, snappy, cert, isLocal), rt, entry...)
}

// LinkConn wraps conn, a link to an npc, with its encryption or compression;
// a local link is used as it is. cert is the certificate of the side that
// opened the link, the TLS server of an encrypted link; it is nil on the
// side that accepted it.
func LinkConn(conn net.Conn, cpt, snappy bool, cert *tls.Certificate, isLocal bool) io.ReadWriteCloser {
	switch {
	case isLocal:
		return conn
	case cpt && cert != nil:
		return crypt.NewTlsServerConn(conn, *cert)
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
		log.Error("local udp port not bound", "addr", localIP, "err", err)
		return
	}
	relay := socks5.DatagramRelay{
		IdleTimeout: timeout,
		OnError:     func(err error) { log.Debug("socks5 udp datagram failed", "err", err) },
	}
	framed := netx.NewFramedConn(serverConn, netx.WithFramedDatagrams())
	if err := relay.Relay(ctx, framed, socks5.NewPacketConn(local)); err != nil {
		log.Debug("socks5 udp relay ended", "err", err)
	}
}

// CopyWaitGroup relays between conn1, a link to an npc, and conn2. cert is
// the certificate of the side that opened the link, as for LinkConn; the
// limits of task apply on that side only.
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, rt *rate.Rate,
	flows []Flow, cert *tls.Certificate, proxyProtocol int, rb []byte, task Entry, isLocal, isFramed bool) {
	var limit *rate.Directional
	if cert != nil && task != nil {
		// Client-side relay wrappers stay out of server-side metering.
		limit = task.RateLimiter()
	}
	connHandle := GetConn(conn1, crypt, snappy, rt, cert, isLocal, limit)
	if isFramed {
		// connHandle only wraps conn1, which every path below closes.
		//noinspection GoResourceLeak
		connHandle = netx.NewFramedConn(netx.WrapConn(connHandle, conn1, netx.WithWrapParentClose()), netx.WithFramedDatagrams())
	}
	if proxyHeader := ProxyProtocolHeader(proxyProtocol, conn2.RemoteAddr(), conn2.LocalAddr()); proxyHeader != nil {
		log.Debug("proxy protocol header sent", "version", proxyProtocol, "src", proxyHeader.Source, "dst", proxyHeader.Destination)
		if _, err := proxyHeader.WriteTo(connHandle); err != nil {
			log.Debug("proxy protocol header not written", "err", err)
			_ = conn1.Close()
			_ = conn2.Close()
			return
		}
	}
	if rb != nil {
		if _, err := connHandle.Write(rb); err != nil {
			log.Debug("pre-read data not written", "err", err)
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
		log.Debug("proxy protocol header without addresses", "src", client, "dst", target, "err", err)
		return &proxyproto.Header{Version: v}
	}
	return h
}
