package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/rate"
)

// socks5UDPQueue is how many client datagrams a UDP association buffers.
const socks5UDPQueue = 1024

// socks5UDPIdleTimeout ends a UDP association and its tunnel without
// datagrams for this long.
var socks5UDPIdleTimeout = socks5.DefaultUDPIdleTimeout

// listenSocks5UDP opens the UDP socket shared by the task's associations.
func listenSocks5UDP(bridge NetBridge, task *model.Tunnel, addr string) (net.PacketConn, error) {
	pc, err := conn.NewUdpConnByAddr(addr)
	if err != nil {
		return nil, err
	}
	var listener net.PacketConn = pc
	if task != nil && task.Client != nil {
		// Visitor datagrams over the task's rates are dropped rather than
		// stalling the tunnels of every session.
		listener = rate.NewVisitorPacketConn(listener, task.Client.Rate, task.RateLimiter())
	}
	if bridge.IsServer() {
		listener = &sourceFilterPacketConn{PacketConn: listener, task: task}
	}
	return listener, nil
}

// sourceFilterPacketConn drops the datagrams of sources that the access lists
// deny, also once an association has started.
type sourceFilterPacketConn struct {
	net.PacketConn
	task *model.Tunnel
}

func (c *sourceFilterPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil || c.task == nil || c.task.Client == nil || !datagramDenied(addr, c.task.Client, &c.task.SourceLists) {
			return n, addr, err
		}
	}
}

func (s *TunnelModeServer) socks5UDPAddr(req *socks5.Request, socket net.Addr) netx.Addr {
	var clientIP net.IP
	if ip, ok := netaddr.ParseIP(req.Conn.RemoteAddr().String()); ok {
		clientIP = netaddr.NormalizeIP(ip.WithZone("").AsSlice())
	}
	return socks5UDPReplyAddr(s.Task, req.Conn, socket, clientIP)
}

// allowsSocks5Datagram applies the destination list of a mixProxy task to the
// destination of a datagram.
func (s *TunnelModeServer) allowsSocks5Datagram(_ *socks5.Request, dst netx.Addr) bool {
	t := s.Task
	if t == nil || t.Mode != "mixProxy" || t.DestAclMode == model.AclOff {
		return true
	}
	return t.AllowsDestination(dst.String())
}

// dialSocks5Packet opens the tunnel of a UDP association. The client end
// takes the datagrams with their SOCKS5 headers and sends the replies so.
func (s *TunnelModeServer) dialSocks5Packet(_ context.Context, req *socks5.Request) (socks5.PacketConn, error) {
	task := s.Task
	if err := validateSocks5UDPTunnelTask(task); err != nil {
		return nil, err
	}
	link := conn.NewLink(
		conn.LinkUDP5,
		"",
		task.Client.Cnf.Crypt,
		task.Client.Cnf.Compress,
		req.Conn.RemoteAddr().String(),
		s.AllowLocalProxy && task.Target.LocalProxy,
	)
	link.Option.Timeout = socks5UDPIdleTimeout
	link.Option.Node = task.Route().Node

	target, err := s.Bridge.Session(task.Client.Id).OpenLink(link, task.Id)
	if err != nil {
		return nil, err
	}
	timeoutConn := netx.NewTimeoutConn(target, link.Option.Timeout)
	// The packet conn owns the framed connection and closes it in Close.
	//noinspection GoResourceLeak
	flowConn := conn.NewFlowConn(timeoutConn, task.Flow, task.Client.Flow)
	return &socks5TunnelPacketConn{
		framed: netx.NewFramedConn(flowConn, netx.WithFramedDatagrams()),
		wbuf:   make([]byte, 0, netx.MaxFramePayload),
		rbuf:   make([]byte, netx.MaxFramePayload),
	}, nil
}

func validateSocks5UDPTunnelTask(task *model.Tunnel) error {
	switch {
	case task == nil:
		return errors.New("nil socks5 udp task")
	case task.Client == nil:
		return errors.New("nil socks5 udp task client")
	case task.Client.Cnf == nil:
		return errors.New("nil socks5 udp client config")
	case task.Target == nil:
		return errors.New("nil socks5 udp target")
	case task.Flow == nil:
		return errors.New("nil socks5 udp task flow")
	case task.Client.Flow == nil:
		return errors.New("nil socks5 udp client flow")
	default:
		return nil
	}
}

// socks5TunnelPacketConn carries the datagrams of one UDP association over
// the tunnel, one SOCKS5 datagram per frame. WriteTo and ReadFrom each run on
// one goroutine, which own wbuf and rbuf.
type socks5TunnelPacketConn struct {
	framed *netx.FramedConn
	wbuf   []byte
	rbuf   []byte
}

func (c *socks5TunnelPacketConn) WriteTo(p []byte, dst netx.Addr) (int, error) {
	b, err := socks5.AppendDatagram(c.wbuf[:0], dst, p)
	if err != nil {
		return 0, err
	}
	if len(b) > netx.MaxFramePayload {
		return 0, fmt.Errorf("socks5 udp datagram too large: %d > %d", len(b), netx.MaxFramePayload)
	}
	c.wbuf = b[:0]
	if _, err := c.framed.Write(b); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *socks5TunnelPacketConn) ReadFrom(p []byte) (int, netx.Addr, error) {
	for {
		n, err := c.framed.Read(c.rbuf)
		if err != nil {
			return 0, netx.Addr{}, err
		}
		src, hl, err := socks5.DecodeDatagram(c.rbuf[:n])
		if err != nil {
			continue // a malformed reply from the client end
		}
		return copy(p, c.rbuf[hl:n]), src, nil
	}
}

func (c *socks5TunnelPacketConn) Close() error {
	return c.framed.Close()
}
