package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxyproto"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/slogx"
)

var log = slogx.Module("conn")

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
	HandleUdp5Policy(ctx, serverConn, timeout, localIP, nil)
}

// UDP5Policy decides the destination of a datagram, addr being host:port. It
// is called with ips nil for a name not yet resolved, where it refuses what
// the name alone refuses, and again with the addresses the name resolved to;
// an IP destination comes with itself as ips.
type UDP5Policy func(addr string, ips []netip.Addr) bool

// HandleUdp5Policy is HandleUdp5 dropping the datagrams allow refuses; nil
// allows all. A name is sent to the address it was judged by.
func HandleUdp5Policy(ctx context.Context, serverConn net.Conn, timeout time.Duration, localIP string, allow UDP5Policy) {
	local, err := net.ListenUDP("udp", netaddr.BuildUDPBindAddr(localIP))
	if err != nil {
		_ = serverConn.Close()
		log.Debug("local udp port not bound", "listen", localIP, "err", err)
		return
	}
	relay := socks5.DatagramRelay{
		IdleTimeout: timeout,
		OnError:     func(err error) { log.Debug("socks5 udp datagram failed", "err", err) },
	}
	pc := socks5.NewPacketConn(local)
	if allow != nil {
		relay.Allow = func(dst netx.Addr) bool {
			if dst.IP.IsValid() {
				return allow(netip.AddrPortFrom(dst.IP, dst.Port).String(), []netip.Addr{dst.IP})
			}
			return allow(dstAddr(dst), nil)
		}
		pc = &judgedPacketConn{PacketConn: pc, allow: allow, resolver: net.DefaultResolver}
	}
	framed := netx.NewFramedConn(serverConn, netx.WithFramedDatagrams())
	if err := relay.Relay(ctx, framed, pc); err != nil {
		log.Debug("socks5 udp relay ended", "err", err)
	}
}

func dstAddr(dst netx.Addr) string {
	return net.JoinHostPort(dst.Name, strconv.Itoa(int(dst.Port)))
}

const (
	udp5LookupTimeout = 3 * time.Second
	udp5NameTTL       = 30 * time.Second
	// udp5NameCache bounds the names one association remembers.
	udp5NameCache = 4096
)

var errUDP5Refused = errors.New("socks5 udp destination refused")

type udp5Name struct {
	ip      netip.Addr // invalid: refused or unresolved
	expires time.Time
}

// judgedPacketConn resolves names itself, so that a datagram goes to an
// address the policy has seen and a later answer of the DNS cannot change it.
type judgedPacketConn struct {
	socks5.PacketConn
	allow    UDP5Policy
	resolver interface {
		LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
	}
	mu    sync.Mutex
	names map[string]udp5Name
}

func (c *judgedPacketConn) WriteTo(p []byte, dst netx.Addr) (int, error) {
	if dst.IP.IsValid() || dst.Name == "" {
		return c.PacketConn.WriteTo(p, dst)
	}
	ip := c.judge(dst)
	if !ip.IsValid() {
		return 0, fmt.Errorf("%w: %s", errUDP5Refused, dst.Name)
	}
	return c.PacketConn.WriteTo(p, netx.Addr{IP: ip, Port: dst.Port})
}

// judge returns the address to send to, invalid if there is none. The rules
// may name ports, so a name is remembered per host:port.
func (c *judgedPacketConn) judge(dst netx.Addr) netip.Addr {
	addr := dstAddr(dst)
	now := time.Now()
	c.mu.Lock()
	n, ok := c.names[addr]
	c.mu.Unlock()
	if ok && now.Before(n.expires) {
		return n.ip
	}
	n = udp5Name{expires: now.Add(udp5NameTTL)}
	ctx, cancel := context.WithTimeout(context.Background(), udp5LookupTimeout)
	ips, err := c.resolver.LookupNetIP(ctx, "ip", dst.Name)
	cancel()
	if err == nil && len(ips) > 0 && c.allow(addr, ips) {
		n.ip = ips[0]
		for _, ip := range ips {
			if ip.Unmap().Is4() { // reachable from every socket family
				n.ip = ip.Unmap()
				break
			}
		}
	}
	c.mu.Lock()
	if c.names == nil || len(c.names) >= udp5NameCache {
		c.names = make(map[string]udp5Name)
	}
	c.names[addr] = n
	c.mu.Unlock()
	return n.ip
}

// CopyWaitGroup relays between conn1, a link to an npc, and conn2. cert is
// the certificate of the side that opened the link, as for LinkConn; the
// limits of task apply on that side only. It returns the bytes written out
// to conn2 and read in from it.
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, rt *rate.Rate,
	flows []Flow, cert *tls.Certificate, proxyProtocol int, rb []byte, task Entry, isLocal, isFramed bool) (out, in int64) {
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
		log.Debug("proxy protocol header sent", "version", proxyProtocol, "addr", proxyHeader.Source)
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
	out, in = relay(connHandle, conn2, flows, task, remote)
	_ = conn1.Close()
	_ = conn2.Close()
	return out, in
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
		log.Debug("proxy protocol header without addresses", "err", err)
		return &proxyproto.Header{Version: v}
	}
	return h
}
