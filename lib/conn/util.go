package conn

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxyproto"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

// GetConn get crypt or snappy conn
func GetConn(conn net.Conn, cpt, snappy bool, rt *rate.Rate, isServer, isLocal bool) io.ReadWriteCloser {
	if !isLocal {
		if cpt {
			if isServer {
				return rate.NewRateConn(crypt.NewTlsServerConn(conn), rt)
			}
			return rate.NewRateConn(crypt.NewTlsClientConn(conn), rt)
		} else if snappy {
			return rate.NewRateConn(NewSnappyConn(conn), rt)
		}
	}
	return rate.NewRateConn(conn, rt)
}

func GetTlsConn(c net.Conn, sni string) (net.Conn, error) {
	serverName := common.RemovePortFromHost(sni)
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         serverName,
	}
	tlsConn := tls.Client(c, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		logs.Error("TLS handshake with backend failed: %v", err)
		_ = tlsConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// GetLenBytes get the assembled amount data(len 4 and content)
func GetLenBytes(buf []byte) (b []byte, err error) {
	raw := bytes.NewBuffer([]byte{})
	if err = binary.Write(raw, binary.LittleEndian, int32(len(buf))); err != nil {
		return
	}
	if err = binary.Write(raw, binary.LittleEndian, buf); err != nil {
		return
	}
	b = raw.Bytes()
	return
}

func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	s := strings.ToLower(strings.ReplaceAll(err.Error(), " ", ""))
	return strings.Contains(s, "timeout")
}

// udp5HeaderRoom is the length of the longest SOCKS5 UDP header for an IP
// source: reserved bytes, fragment number, address type, IPv6 address and
// port. Replies are read behind this much room so that their header can be
// written in front of them in place.
const udp5HeaderRoom = 3 + 1 + 16 + 2

// HandleUdp5 relays the SOCKS5 UDP datagrams of serverConn, one per frame,
// through a local UDP socket: each datagram's payload goes to its
// destination, and each datagram received goes back with its source as the
// address.
func HandleUdp5(ctx context.Context, serverConn net.Conn, timeout time.Duration, localIP string) {
	// Wrap the TCP tunnel with timeout and framed I/O.
	defer func() { _ = serverConn.Close() }()
	timeoutConn := netx.NewTimeoutConn(serverConn, timeout)
	defer func() { _ = timeoutConn.Close() }()
	framed := netx.NewFramedConn(timeoutConn, netx.WithFramedDatagrams())

	// Bind one local UDP socket for all outbound UDP traffic.
	// Using nil lets the kernel pick family/port.
	local, err := net.ListenUDP("udp", common.BuildUDPBindAddr(localIP))
	if err != nil {
		logs.Error("bind local udp port error %v", err)
		return
	}
	defer func() { _ = local.Close() }()
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Internet -> tunnel: read UDP responses, prefix the SOCKS5 UDP header,
	// send through the tunnel.
	go func() {
		defer cancel()
		buf := common.BufPool.Get()
		defer common.BufPool.Put(buf)

		for {
			select {
			case <-relayCtx.Done():
				return
			default:
			}

			n, src, err := local.ReadFromUDPAddrPort(buf[udp5HeaderRoom:])
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					logs.Debug("local UDP closed, exiting goroutine")
					return
				}
				if IsTimeout(err) {
					logs.Debug("temporary UDP read error, retrying: %v", err)
					continue
				}
				logs.Debug("read data from remote server error %v", err)
				return
			}

			from := socks5.Addr{IP: src.Addr().Unmap(), Port: src.Port()}
			start := udp5HeaderRoom - (3 + 1 + 4 + 2)
			if from.IP.Is6() {
				start = 0
			}
			// The header fills buf[start:udp5HeaderRoom] exactly, so the
			// payload behind it stays in place. An IP address cannot fail.
			_, _ = socks5.AppendDatagram(buf[start:start], from, nil)
			if _, err := framed.Write(buf[start : udp5HeaderRoom+n]); err != nil {
				logs.Debug("write data to tunnel error %v", err)
				return
			}
		}
	}()

	// Tunnel -> internet: read one datagram per frame and send its payload
	// via the local UDP socket.
	frameBuf := common.BufPool.Get()
	defer common.BufPool.Put(frameBuf)

	for {
		select {
		case <-relayCtx.Done():
			return
		default:
		}

		n, err := framed.Read(frameBuf)
		if err != nil {
			logs.Debug("read udp frame from tunnel error %v", err)
			return
		}

		dst, hlen, err := socks5.DecodeDatagram(frameBuf[:n])
		if err != nil {
			// Drop malformed or fragmented packets.
			logs.Debug("parse socks5 udp packet error %v", err)
			continue
		}
		payload := frameBuf[hlen:n]

		if dst.IP.IsValid() {
			_, err = local.WriteToUDPAddrPort(payload, netip.AddrPortFrom(dst.IP, dst.Port))
		} else {
			var rAddr *net.UDPAddr
			if rAddr, err = net.ResolveUDPAddr("udp", dst.String()); err != nil {
				logs.Debug("resolve dest addr %q error %v", dst, err)
				continue
			}
			_, err = local.WriteToUDP(payload, rAddr)
		}
		if err != nil {
			if IsTimeout(err) {
				logs.Debug("temporary UDP write error to %v, retrying: %v", dst, err)
				continue
			}
			logs.Debug("write udp to %v error %v", dst, err)
			return
		}
	}
}

func WriteACK(c net.Conn, timeout time.Duration) error {
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := c.Write([]byte(common.ConnAck))
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

func ReadACK(c net.Conn, timeout time.Duration) error {
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, len(common.ConnAck))
	_, err := io.ReadFull(c, buf)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	if string(buf) != common.ConnAck {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// CopyWaitGroup conn1 mux conn
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, rate *rate.Rate,
	flows []*file.Flow, isServer bool, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal, isFramed bool) {
	connHandle := GetConn(conn1, crypt, snappy, rate, isServer, isLocal)
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

func GetRealIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			for _, p := range strings.Split(v, ",") {
				if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
					return ip
				}
			}
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return host
		}
		return r.RemoteAddr
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, p := range strings.Split(xff, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
				return ip
			}
		}
	}

	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		for _, p := range strings.Split(xrip, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
				return ip
			}
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
