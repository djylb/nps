package bridge

import (
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
)

// StartTunnel opens the listeners of the enabled bridge transports and
// serves them in the background. It returns the error of the first listener
// that cannot be opened. A listener that fails later stops accepting and
// reports its error to failed, which decides whether nps goes on.
func (s *Bridge) StartTunnel(failed func(error)) error {
	n := connection.Network()
	var err error
	// tcp
	if s.VirtualTcpListener, err = virtualBridgeListener(ServerTcpEnable, connection.KindBridgeTCP, failed); err != nil {
		return err
	}
	// tls
	if s.VirtualTlsListener, err = virtualBridgeListener(ServerTlsEnable, connection.KindBridgeTLS, failed); err != nil {
		return err
	}
	// ws
	if s.VirtualWsListener, err = virtualBridgeListener(ServerWsEnable, connection.KindBridgeWS, failed); err != nil {
		return err
	}
	// wss
	if s.VirtualWssListener, err = virtualBridgeListener(ServerWssEnable, connection.KindBridgeWSS, failed); err != nil {
		return err
	}

	// kcp
	if ServerKcpEnable {
		logs.Info("Server start, the bridge type is kcp, the bridge port is %d", n.BridgeKCPPort)
		l, err := conn.ListenKCP(net.JoinHostPort(n.BridgeKCPIP, strconv.Itoa(n.BridgeKCPPort)))
		if err != nil {
			return fmt.Errorf("kcp bridge listener: %w", err)
		}
		go func() {
			err := conn.ServeKCP(l, func(c net.Conn) {
				s.CliProcess(conn.NewConn(c), conn.TransportKCP)
			})
			failed(fmt.Errorf("kcp bridge listener: %w", err))
		}()
	}

	// quic
	if ServerQuicEnable {
		logs.Info("Server start, the bridge type is quic, the bridge port is %d", n.BridgeQUICPort)
		quicConfig := &quic.Config{
			KeepAlivePeriod:    time.Duration(n.QUICKeepAlivePeriod) * time.Second,
			MaxIdleTimeout:     time.Duration(n.QUICMaxIdleTimeout) * time.Second,
			MaxIncomingStreams: n.QUICMaxIncomingStreams,
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{crypt.GetCert()},
			NextProtos:   n.QUICALPNList,
		}
		l, err := conn.ListenQUIC(net.JoinHostPort(n.BridgeQUICIP, strconv.Itoa(n.BridgeQUICPort)), tlsCfg, quicConfig)
		if err != nil {
			return fmt.Errorf("quic bridge listener: %w", err)
		}
		go func() {
			err := conn.ServeQUIC(l, func(c net.Conn) {
				s.CliProcess(conn.NewConn(c), conn.TransportQUIC)
			})
			failed(fmt.Errorf("quic bridge listener: %w", err))
		}()
	}

	go s.ping()
	go conn.Accept(s.VirtualTcpListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), conn.TransportTCP)
	})
	go conn.Accept(s.VirtualTlsListener, func(c net.Conn) {
		s.CliProcess(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{crypt.GetCert()}})), conn.TransportTLS)
	})
	wsLn := conn.NewWSListener(s.VirtualWsListener, n.BridgePath, n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	go conn.Accept(wsLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), conn.TransportWS)
	})
	wssLn := conn.NewWSSListener(s.VirtualWssListener, n.BridgePath, crypt.GetCert(), n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	go conn.Accept(wssLn, func(c net.Conn) {
		s.CliProcess(conn.NewConn(c), conn.TransportWSS)
	})
	return nil
}

// virtualBridgeListener returns the in-memory listener of one bridge
// transport. When the transport is enabled it opens the real listener of
// kind k, reports its address and feeds it the connections that listener
// accepts, reporting to failed if that listener stops on an error; bridge://
// links dial the in-memory listener either way.
func virtualBridgeListener(enabled bool, k connection.Kind, failed func(error)) (*netx.ChanListener, error) {
	if !enabled {
		return conn.NewVirtualListener(nil), nil
	}
	ln, err := connection.Listen(k)
	if err != nil {
		return nil, err
	}
	vl := conn.NewVirtualListener(ln.Addr())
	go func() {
		if err := conn.Accept(ln, func(c net.Conn) { conn.ServeVirtual(vl, c) }); err != nil {
			failed(fmt.Errorf("%v listener: %w", k, err))
		}
	}()
	return vl, nil
}
