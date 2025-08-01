package conn

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/goroutine"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
	"github.com/xtaci/kcp-go/v5"
)

// GetConn get crypt or snappy conn
func GetConn(conn net.Conn, cpt, snappy bool, rt *rate.Rate, isServer bool) io.ReadWriteCloser {
	if cpt {
		if isServer {
			return rate.NewRateConn(crypt.NewTlsServerConn(conn), rt)
		}
		return rate.NewRateConn(crypt.NewTlsClientConn(conn), rt)
	} else if snappy {
		return rate.NewRateConn(NewSnappyConn(conn), rt)
	}
	return rate.NewRateConn(conn, rt)
}

func GetTlsConn(c net.Conn, sni string) (net.Conn, error) {
	serverName := common.RemovePortFromHost(sni)
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         serverName,
	}
	c = tls.Client(c, tlsConf)
	if err := c.(*tls.Conn).Handshake(); err != nil {
		logs.Error("TLS handshake with backend failed: %v", err)
		return nil, err
	}
	return c, nil
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

// SetUdpSession udp connection setting
func SetUdpSession(sess *kcp.UDPSession) {
	sess.SetStreamMode(true)
	sess.SetWindowSize(1024, 1024)
	_ = sess.SetReadBuffer(64 * 1024)
	_ = sess.SetWriteBuffer(64 * 1024)
	sess.SetNoDelay(1, 10, 2, 1)
	sess.SetMtu(1600)
	sess.SetACKNoDelay(true)
	sess.SetWriteDelay(false)
}

// CopyWaitGroup conn1 mux conn
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, rate *rate.Rate,
	flows []*file.Flow, isServer bool, proxyProtocol int, rb []byte, task *file.Tunnel) {
	connHandle := GetConn(conn1, crypt, snappy, rate, isServer)
	proxyHeader := BuildProxyProtocolHeader(conn2, proxyProtocol)
	if proxyHeader != nil {
		logs.Debug("Sending Proxy Protocol v%d header to backend: %v", proxyProtocol, proxyHeader)
		_, _ = connHandle.Write(proxyHeader)
	}
	if rb != nil {
		_, _ = connHandle.Write(rb)
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	err := goroutine.CopyConnsPool.Invoke(goroutine.NewConns(connHandle, conn2, flows, wg, task))
	if err != nil {
		logs.Error("CopyConnsPool.Invoke failed: %v", err)
		wg.Done()
		_ = connHandle.Close()
		_ = conn2.Close()
	}
	wg.Wait()
}

func BuildProxyProtocolV1Header(clientAddr, targetAddr net.Addr) []byte {
	var (
		protocol           = "UNKNOWN"
		clientIP, targetIP string
		srcPort, dstPort   int
	)

	switch c := clientAddr.(type) {
	case *net.TCPAddr:
		if t, ok := targetAddr.(*net.TCPAddr); ok {
			clientIP, targetIP = c.IP.String(), t.IP.String()
			srcPort, dstPort = c.Port, t.Port
			if c.IP.To4() != nil {
				protocol = "TCP4"
			} else {
				protocol = "TCP6"
			}
		}
	case *net.UDPAddr:
		if u, ok := targetAddr.(*net.UDPAddr); ok {
			clientIP, targetIP = c.IP.String(), u.IP.String()
			srcPort, dstPort = c.Port, u.Port
			if c.IP.To4() != nil {
				protocol = "TCP4"
			} else {
				protocol = "TCP6"
			}
		}
	}

	if protocol == "UNKNOWN" {
		return []byte("PROXY UNKNOWN\r\n")
	}

	header := "PROXY " + protocol + " " + clientIP + " " + targetIP + " " +
		strconv.Itoa(srcPort) + " " + strconv.Itoa(dstPort) + "\r\n"
	return []byte(header)
}

func BuildProxyProtocolV2Header(clientAddr, targetAddr net.Addr) []byte {
	const sig = "\r\n\r\n\000\r\nQUIT\n" // 12-byte v2 signature
	var (
		header           []byte
		famProto         byte
		addrBytes        uint16
		srcIP, dstIP     net.IP
		srcPort, dstPort uint16
	)

	switch c := clientAddr.(type) {
	case *net.TCPAddr:
		t := targetAddr.(*net.TCPAddr)
		srcIP, dstIP = c.IP, t.IP
		srcPort, dstPort = uint16(c.Port), uint16(t.Port)
		if c.IP.To4() != nil {
			famProto, addrBytes = 0x11, 12 // TCPv4
		} else {
			famProto, addrBytes = 0x21, 36 // TCPv6
		}
	case *net.UDPAddr:
		u := targetAddr.(*net.UDPAddr)
		srcIP, dstIP = c.IP, u.IP
		srcPort, dstPort = uint16(c.Port), uint16(u.Port)
		if c.IP.To4() != nil {
			famProto, addrBytes = 0x12, 12 // UDPv4
		} else {
			famProto, addrBytes = 0x22, 36 // UDPv6
		}
	}

	header = make([]byte, 16+addrBytes)
	copy(header[:12], sig)
	header[12] = 0x21 // v2 + PROXY
	header[13] = famProto
	binary.BigEndian.PutUint16(header[14:16], addrBytes)

	if addrBytes == 12 { // IPv4
		copy(header[16:20], srcIP.To4())
		copy(header[20:24], dstIP.To4())
		binary.BigEndian.PutUint16(header[24:26], srcPort)
		binary.BigEndian.PutUint16(header[26:28], dstPort)
	} else { // IPv6
		copy(header[16:32], srcIP.To16())
		copy(header[32:48], dstIP.To16())
		binary.BigEndian.PutUint16(header[48:50], srcPort)
		binary.BigEndian.PutUint16(header[50:52], dstPort)
	}
	return header
}

func BuildProxyProtocolHeader(c net.Conn, proxyProtocol int) []byte {
	if proxyProtocol == 0 {
		return nil
	}
	clientAddr := c.RemoteAddr()
	targetAddr := c.LocalAddr()

	if proxyProtocol == 2 {
		return BuildProxyProtocolV2Header(clientAddr, targetAddr)
	}
	if proxyProtocol == 1 {
		return BuildProxyProtocolV1Header(clientAddr, targetAddr)
	}
	return nil
}

func BuildProxyProtocolHeaderByAddr(clientAddr, targetAddr net.Addr, proxyProtocol int) []byte {
	if proxyProtocol == 0 {
		return nil
	}

	targetAddr = normalizeTarget(clientAddr, targetAddr)

	switch proxyProtocol {
	case 2:
		return BuildProxyProtocolV2Header(clientAddr, targetAddr)
	case 1:
		return BuildProxyProtocolV1Header(clientAddr, targetAddr)
	default:
		return nil
	}
}

func normalizeTarget(src, dst net.Addr) net.Addr {
	switch s := src.(type) {

	// TCP
	case *net.TCPAddr:
		d, _ := dst.(*net.TCPAddr)
		if d == nil {
			d = &net.TCPAddr{Port: 0}
		}
		srcIsV4 := s.IP.To4() != nil
		dstIsV4 := d.IP != nil && d.IP.To4() != nil

		switch {
		case srcIsV4 && !dstIsV4:
			d.IP = net.IPv4zero
		case !srcIsV4 && dstIsV4:
			d.IP = append(net.IPv6zero[:12], d.IP.To4()...)
		case d.IP == nil || d.IP.IsUnspecified():
			if srcIsV4 {
				d.IP = net.IPv4zero
			} else {
				d.IP = net.IPv6zero
			}
		}
		return d

	// UDP
	case *net.UDPAddr:
		d, _ := dst.(*net.UDPAddr)
		if d == nil {
			d = &net.UDPAddr{Port: 0}
		}
		srcIsV4 := s.IP.To4() != nil
		dstIsV4 := d.IP != nil && d.IP.To4() != nil

		switch {
		case srcIsV4 && !dstIsV4:
			d.IP = net.IPv4zero
		case !srcIsV4 && dstIsV4:
			d.IP = append(net.IPv6zero[:12], d.IP.To4()...)
		case d.IP == nil || d.IP.IsUnspecified():
			if srcIsV4 {
				d.IP = net.IPv4zero
			} else {
				d.IP = net.IPv6zero
			}
		}
		return d

	// Other
	default:
		return dst
	}
}
