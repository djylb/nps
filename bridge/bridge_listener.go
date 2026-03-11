package bridge

import (
	"crypto/tls"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
)

func (s *Bridge) StartTunnel() error {
	go s.ping()
	// tcp
	s.VirtualTcpListener = conn.NewVirtualListener(nil)
	go conn.Accept(s.VirtualTcpListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), common.CONN_TCP)
	})
	if ServerTcpEnable {
		go func() {
			listener, err := connection.GetBridgeTcpListener()
			if err != nil {
				logs.Error("%v", err)
				os.Exit(0)
				return
			}
			s.VirtualTcpListener.SetAddr(listener.Addr())
			conn.Accept(listener, s.VirtualTcpListener.ServeVirtual)
		}()
	}

	// tls
	s.VirtualTlsListener = conn.NewVirtualListener(nil)
	go conn.Accept(s.VirtualTlsListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{crypt.GetCert()}})), common.CONN_TLS)
	})
	if ServerTlsEnable {
		go func() {
			tlsListener, tlsErr := connection.GetBridgeTlsListener()
			if tlsErr != nil {
				logs.Error("%v", tlsErr)
				os.Exit(0)
				return
			}
			s.VirtualTlsListener.SetAddr(tlsListener.Addr())
			conn.Accept(tlsListener, s.VirtualTlsListener.ServeVirtual)
		}()
	}

	// ws
	s.VirtualWsListener = conn.NewVirtualListener(nil)
	wsLn := conn.NewWSListener(s.VirtualWsListener, connection.BridgePath, connection.BridgeTrustedIps, connection.BridgeRealIpHeader)
	go conn.Accept(wsLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), common.CONN_WS)
	})
	if ServerWsEnable {
		go func() {
			wsListener, wsErr := connection.GetBridgeWsListener()
			if wsErr != nil {
				logs.Error("%v", wsErr)
				os.Exit(0)
				return
			}
			s.VirtualWsListener.SetAddr(wsListener.Addr())
			conn.Accept(wsListener, s.VirtualWsListener.ServeVirtual)
		}()
	}

	// wss
	s.VirtualWssListener = conn.NewVirtualListener(nil)
	wssLn := conn.NewWSSListener(s.VirtualWssListener, connection.BridgePath, crypt.GetCert(), connection.BridgeTrustedIps, connection.BridgeRealIpHeader)
	go conn.Accept(wssLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), common.CONN_WSS)
	})
	if ServerWssEnable {
		go func() {
			wssListener, wssErr := connection.GetBridgeWssListener()
			if wssErr != nil {
				logs.Error("%v", wssErr)
				os.Exit(0)
				return
			}
			s.VirtualWssListener.SetAddr(wssListener.Addr())
			conn.Accept(wssListener, s.VirtualWssListener.ServeVirtual)
		}()
	}
	// kcp
	if ServerKcpEnable {
		logs.Info("Server start, the bridge type is kcp, the bridge port is %d", connection.BridgeKcpPort)
		go func() {
			//bridgeKcp := *s
			//bridgeKcp.tunnelType = "kcp"
			err := conn.NewKcpListenerAndProcess(common.BuildAddress(connection.BridgeKcpIp, strconv.Itoa(connection.BridgeKcpPort)), func(c net.Conn) {
				s.CliProcess(conn.NewConn(c), "kcp")
			})
			if err != nil {
				logs.Error("KCP listener error: %v", err)
			}
		}()
	}

	// quic
	if ServerQuicEnable {
		logs.Info("Server start, the bridge type is quic, the bridge port is %d", connection.BridgeQuicPort)

		quicConfig := &quic.Config{
			KeepAlivePeriod:    time.Duration(connection.QuicKeepAliveSec) * time.Second,
			MaxIdleTimeout:     time.Duration(connection.QuicIdleTimeoutSec) * time.Second,
			MaxIncomingStreams: connection.QuicMaxStreams,
		}
		go func() {
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{crypt.GetCert()},
			}
			tlsCfg.NextProtos = connection.QuicAlpn
			addr := common.BuildAddress(connection.BridgeQuicIp, strconv.Itoa(connection.BridgeQuicPort))
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
