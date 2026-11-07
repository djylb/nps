package conn

import (
	"crypto/rand"
	"encoding/binary"
	"net"

	"github.com/djylb/nps/lib/netaddr"
	"github.com/xtaci/kcp-go/v5"
)

// SetUdpSession udp connection setting
func SetUdpSession(sess *kcp.UDPSession) {
	//sess.SetStreamMode(true)
	sess.SetWindowSize(512, 512)
	_ = sess.SetReadBuffer(128 * 1024)
	_ = sess.SetWriteBuffer(128 * 1024)
	sess.SetNoDelay(1, 10, 3, 1)
	sess.SetMtu(1350)
	sess.SetACKNoDelay(true)
	sess.SetWriteDelay(false)
}

// DialKCPWithLocalIP creates a KCP connection with optional local IP binding.
func DialKCPWithLocalIP(raddr string, localIP string) (*kcp.UDPSession, error) {
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return nil, err
	}

	network := "udp4"
	if udpaddr.IP == nil || udpaddr.IP.To4() == nil {
		network = "udp"
	}

	conn, err := net.ListenUDP(network, netaddr.BuildUDPBindAddr(localIP))
	if err != nil {
		return nil, err
	}

	sess, err := NewKCPSessionWithConn(udpaddr, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return sess, nil
}

// NewKCPSessionWithConn creates a KCP session based on an existing PacketConn.
func NewKCPSessionWithConn(raddr *net.UDPAddr, pc net.PacketConn) (*kcp.UDPSession, error) {
	var convid uint32

	if err := binary.Read(rand.Reader, binary.LittleEndian, &convid); err != nil {
		return nil, err
	}

	sess, err := kcp.NewConn4(convid, raddr, nil, 10, 3, true, pc)
	if err != nil {
		return nil, err
	}

	SetUdpSession(sess)
	return sess, nil
}
