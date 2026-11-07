package proxy

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx"
	"github.com/djylb/netx/httpproxy"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

type TunnelModeServer struct {
	*BaseServer
	process           process
	address           string
	listener          net.Listener
	socks5            *socks5.Server
	httpProxy         *httpproxy.Server
	activeConnections netx.ConnGroup
}

// NewTunnelModeServer tcp|host|mixproxy
func NewTunnelModeServer(process process, bridge NetBridge, task *model.Tunnel, allowLocalProxy bool) *TunnelModeServer {
	s := &TunnelModeServer{
		BaseServer:        NewBaseServer(bridge, task, allowLocalProxy),
		process:           process,
		activeConnections: netx.ConnGroup{},
	}
	s.socks5 = newSocks5Server(s)
	s.httpProxy = newHTTPProxyServer(s)
	return s
}

// Bind opens the TCP listener and, for SOCKS5, the UDP associate socket.
func (s *TunnelModeServer) Bind() error {
	return s.BindLocked(func() error {
		if s.listener != nil {
			return nil
		}
		if s.Task.ServerIp == "" {
			s.Task.ServerIp = "0.0.0.0"
		}
		s.address = common.BuildAddress(s.Task.ServerIp, strconv.Itoa(s.Task.Port))

		var udp net.PacketConn
		if s.usesSocks5UDP() {
			var err error
			if udp, err = listenSocks5UDP(s.Bridge, s.Task, s.address); err != nil {
				return err
			}
		}
		listen := func(address string) (net.Listener, error) { return net.Listen("tcp", address) }
		if s.Task.Mode == "tcpTrans" || s.Task.Mode == "p2pt" {
			listen = listenTransparent
		}
		listener, err := listen(s.address)
		if err != nil {
			if udp != nil {
				_ = udp.Close()
			}
			return err
		}
		s.socks5.UDPSocket = udp
		s.listener = listener
		return nil
	})
}

func (s *TunnelModeServer) Start() error {
	if err := s.Bind(); err != nil {
		return err
	}
	conn.Accept(s.listener, s.handleConn)
	_ = s.socks5.Close()
	_ = s.httpProxy.Close()
	return nil
}

func (s *TunnelModeServer) Close() error {
	var listener net.Listener
	s.MarkClosed(func() {
		listener = s.listener
	})
	_ = s.activeConnections.Close()
	_ = s.socks5.Close()
	_ = s.httpProxy.Close()
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (s *TunnelModeServer) ServeVirtual(c net.Conn) {
	if c != nil {
		go s.handleConn(c)
	}
}

func (s *TunnelModeServer) DialVirtual(rAddr string) (net.Conn, error) {
	remote, err := conn.ParseTCPAddr(rAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid remote addr %q: %w", rAddr, err)
	}
	local, _ := conn.ParseTCPAddr(s.address)
	a, b := net.Pipe()
	// handleConn owns the server end and closes it.
	//noinspection GoResourceLeak
	go s.handleConn(netx.NewAddrOverrideConn(b, local, remote))
	return a, nil
}

func (s *TunnelModeServer) handleConn(c net.Conn) {
	if c == nil {
		return
	}
	remove, err := s.activeConnections.Add(c)
	if err != nil {
		_ = c.Close()
		return
	}
	defer func() {
		remove()
		_ = c.Close()
	}()

	if s.Bridge.IsServer() {
		release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
		if err != nil {
			logs.Warn("client Id %d, task Id %d, error %v, when tcp connection", s.Task.Client.Id, s.Task.Id, err)
			_ = c.Close()
			return
		}
		defer release()
	}
	logs.Trace("new tcp connection,local port %d,client %d,remote address %v", s.Task.Port, s.Task.Client.Id, c.RemoteAddr())

	_ = s.process(conn.NewConn(c), s)
}

func (s *TunnelModeServer) usesSocks5UDP() bool {
	if s == nil || s.Task == nil {
		return false
	}
	_, socksEnabled := effectiveMixProxyFlags(s.Task)
	return socksEnabled
}

type process func(c *conn.Conn, s *TunnelModeServer) error

// ProcessTunnel tcp proxy
func ProcessTunnel(c *conn.Conn, s *TunnelModeServer) error {
	flows := []conn.Flow{s.Task.Flow, s.Task.Client.Flow}
	err := s.DealTarget(c, s.Task.Client, nil, common.ConnTCP, nil, flows, s.Task)
	if !errors.Is(err, model.ErrNoTarget) {
		return err
	}
	if s.Task.Mode != "file" && s.Bridge.IsServer() {
		_ = c.Close()
		logs.Warn("tcp port %d, client Id %d, task Id %d connect error %v", s.Task.Port, s.Task.Client.Id, s.Task.Id, err)
		return err
	}
	return s.DealClient(c, s.Task.Client, "", nil, common.ConnTCP, nil, flows, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
}
