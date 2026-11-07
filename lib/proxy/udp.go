package proxy

import (
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/rate"
)

const (
	// udpSessionIdleTimeout ends a session once neither direction has
	// carried a datagram for this long.
	udpSessionIdleTimeout = 60 * time.Second
	// udpSessionQueue is how many datagrams of a session wait to be relayed.
	udpSessionQueue = 1024
)

type UdpModeServer struct {
	*BaseServer
	listener *netx.PacketListener
}

func NewUdpModeServer(bridge NetBridge, task *model.Tunnel, allowLocalProxy bool) *UdpModeServer {
	return &UdpModeServer{BaseServer: NewBaseServer(bridge, task, allowLocalProxy)}
}

// Bind opens the UDP socket. Each visitor address becomes a session; the
// datagrams of all of them are limited by the task's rates, dropping those
// over the limits.
func (s *UdpModeServer) Bind() error {
	return s.BindLocked(func() error {
		if s.listener != nil {
			return nil
		}
		if s.Task.ServerIp == "" {
			s.Task.ServerIp = "0.0.0.0"
		}
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(s.Task.ServerIp), Port: s.Task.Port})
		if err != nil {
			return err
		}
		pc := rate.NewVisitorPacketConn(udp, s.Task.Client.Rate, s.Task.RateLimiter())
		s.listener = netx.NewPacketListener(pc,
			netx.WithPacketAcceptFilter(func(peer net.Addr, _ []byte) bool {
				return !s.Bridge.IsServer() || !datagramDenied(peer, s.Task.Client, &s.Task.SourceLists)
			}),
			netx.WithPacketIdleTimeout(udpSessionIdleTimeout),
			netx.WithPacketQueue(udpSessionQueue),
		)
		return nil
	})
}

func (s *UdpModeServer) Start() error {
	if err := s.Bind(); err != nil {
		return err
	}
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return nil
		}
		logs.Trace("New udp packet from client %d: %v", s.Task.Client.Id, c.RemoteAddr())
		go s.serve(c)
	}
}

// serve relays the datagrams of visitor, one session, through a link to the
// client until either side ends or the session idles out.
func (s *UdpModeServer) serve(visitor net.Conn) {
	defer func() { _ = visitor.Close() }()
	if s.Bridge.IsServer() {
		release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
		if err != nil {
			logs.Warn("client Id %d, task Id %d flow/conn limit: %v", s.Task.Client.Id, s.Task.Id, err)
			return
		}
		defer release()
		if err := conn.CheckFlowLimits(s.Task.Flow, "Task"); err != nil {
			logs.Warn("client Id %d, task Id %d flow/conn limit: %v", s.Task.Client.Id, s.Task.Id, err)
			return
		}
	}
	addr := visitor.RemoteAddr()
	route := s.Task.Route()
	isLocal := s.AllowLocalProxy && route.Target.LocalProxy || s.Task.Client.Id < 0
	link := conn.NewLink(conn.LinkUDP, route.Target.TargetStr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, addr.String(), isLocal)
	link.Option.Node = route.Node
	clientConn, err := s.Bridge.Session(s.Task.Client.Id).OpenLink(link, s.Task.Id)
	if err != nil {
		logs.Trace("OpenLink error: %v", err)
		return
	}
	target := conn.LinkConn(clientConn, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, true, isLocal)
	var tunnel net.Conn = conn.NewFlowConn(target, s.Task.Flow, s.Task.Client.Flow)
	if !isLocal || !s.Bridge.IsServer() {
		tunnel = netx.NewFramedConn(tunnel, netx.WithFramedDatagrams())
	}
	defer func() { _ = tunnel.Close() }()

	// Back channel: closing visitor, when the session ends, stops the
	// forward loop below, and closing tunnel stops this one.
	go func() {
		defer func() { _ = visitor.Close() }()
		// One datagram per read, which a short buffer would truncate.
		buf := conn.BufPool.Get()
		defer conn.BufPool.Put(buf)
		for {
			n, err := tunnel.Read(buf)
			if err != nil {
				logs.Trace("back-channel read error: %v", err)
				return
			}
			if _, err := visitor.Write(buf[:n]); err != nil {
				logs.Warn("error writing back to client: %v", err)
				return
			}
		}
	}()

	buf := conn.BufPool.Get()
	defer conn.BufPool.Put(buf)
	// The PROXY header, if any, goes in front of the first datagram only.
	var header []byte
	if hdr := conn.ProxyProtocolHeader(route.Target.ProxyProtocol, addr, &net.UDPAddr{Port: s.Task.Port}); hdr != nil {
		if header, err = hdr.AppendBinary(nil); err != nil {
			logs.Warn("build PROXY protocol header for %v: %v", addr, err)
			header = nil
		}
	}
	for {
		n, err := visitor.Read(buf[len(header):])
		if err != nil {
			if !netx.IsClosed(err) {
				logs.Trace("udp session %v ended: %v", addr, err)
			}
			return
		}
		if header != nil {
			copy(buf, header)
			n += len(header)
			if _, err := tunnel.Write(buf[:n]); err != nil {
				return
			}
			// Later datagrams are read to the start of buf again.
			header = nil
			continue
		}
		if _, err := tunnel.Write(buf[:n]); err != nil {
			return
		}
	}
}

func (s *UdpModeServer) Close() error {
	var listener *netx.PacketListener
	s.MarkClosed(func() { listener = s.listener })
	if listener != nil {
		_ = listener.Close()
	}
	return nil
}
