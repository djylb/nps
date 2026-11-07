package bridge

import (
	"crypto/tls"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
)

func (s *Bridge) StartTunnel() error {
	go s.ping()
	// tcp
	s.VirtualTcpListener = virtualBridgeListener(ServerTcpEnable, connection.GetBridgeTcpListener)
	go conn.Accept(s.VirtualTcpListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), model.ConnTCP)
	})

	// tls
	s.VirtualTlsListener = virtualBridgeListener(ServerTlsEnable, connection.GetBridgeTlsListener)
	go conn.Accept(s.VirtualTlsListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{crypt.GetCert()}})), model.ConnTLS)
	})

	// ws
	s.VirtualWsListener = virtualBridgeListener(ServerWsEnable, connection.GetBridgeWsListener)
	wsLn := conn.NewWSListener(s.VirtualWsListener, connection.Network().BridgePath, connection.Network().BridgeTrustedIPs, connection.Network().BridgeRealIPHeader)
	go conn.Accept(wsLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), model.ConnWS)
	})

	// wss
	s.VirtualWssListener = virtualBridgeListener(ServerWssEnable, connection.GetBridgeWssListener)
	wssLn := conn.NewWSSListener(s.VirtualWssListener, connection.Network().BridgePath, crypt.GetCert(), connection.Network().BridgeTrustedIPs, connection.Network().BridgeRealIPHeader)
	go conn.Accept(wssLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), model.ConnWSS)
	})
	// kcp
	if ServerKcpEnable {
		logs.Info("Server start, the bridge type is kcp, the bridge port is %d", connection.Network().BridgeKCPPort)
		go func() {
			//bridgeKcp := *s
			//bridgeKcp.tunnelType = "kcp"
			err := conn.NewKcpListenerAndProcess(net.JoinHostPort(connection.Network().BridgeKCPIP, strconv.Itoa(connection.Network().BridgeKCPPort)), func(c net.Conn) {
				s.CliProcess(conn.NewConn(c), "kcp")
			})
			if err != nil {
				logs.Error("KCP listener error: %v", err)
			}
		}()
	}

	// quic
	if ServerQuicEnable {
		logs.Info("Server start, the bridge type is quic, the bridge port is %d", connection.Network().BridgeQUICPort)

		quicConfig := &quic.Config{
			KeepAlivePeriod:    time.Duration(connection.Network().QUICKeepAlivePeriod) * time.Second,
			MaxIdleTimeout:     time.Duration(connection.Network().QUICMaxIdleTimeout) * time.Second,
			MaxIncomingStreams: connection.Network().QUICMaxIncomingStreams,
		}
		go func() {
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{crypt.GetCert()},
			}
			tlsCfg.NextProtos = connection.Network().QUICALPNList
			addr := net.JoinHostPort(connection.Network().BridgeQUICIP, strconv.Itoa(connection.Network().BridgeQUICPort))
			err := conn.NewQuicListenerAndProcess(addr, tlsCfg, quicConfig, func(c net.Conn) {
				s.CliProcess(conn.NewConn(c), "quic")
			})
			if err != nil {
				logs.Error("QUIC listener error: %v", err)
			}
		}()
	}

	return nil
}

// virtualBridgeListener returns the in-memory listener of one bridge
// transport. When the transport is enabled it opens the real listener with
// open, reports its address and feeds it the connections that listener
// accepts; bridge:// links dial the in-memory listener either way.
func virtualBridgeListener(enabled bool, open func() (net.Listener, error)) *netx.ChanListener {
	if !enabled {
		return conn.NewVirtualListener(nil)
	}
	ln, err := open()
	if err != nil {
		logs.Error("%v", err)
		os.Exit(0)
	}
	vl := conn.NewVirtualListener(ln.Addr())
	go conn.Accept(ln, func(c net.Conn) {
		conn.ServeVirtual(vl, c)
	})
	return vl
}
