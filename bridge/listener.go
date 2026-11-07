package bridge

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
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

// ErrClosed is returned by StartTunnel on a bridge that is closed.
var ErrClosed = errors.New("bridge closed")

// Transports says which bridge transports listen on a port of their own;
// bridge:// targets reach the TCP, TLS, WS and WSS transports either way.
type Transports struct {
	TCP, TLS, WS, WSS, KCP, QUIC bool
}

// StartTunnel opens the listeners of the transports in t and serves them in
// the background. It returns the error of the first listener that cannot be
// opened; Close then closes the ones that were. A listener that fails later
// stops accepting and reports its error to failed, which decides whether nps
// goes on; nothing is reported once Close has started.
func (s *Bridge) StartTunnel(t Transports, failed func(error)) error {
	if s.ctx.Err() != nil {
		return ErrClosed
	}
	report := func(err error) {
		if s.ctx.Err() == nil {
			failed(err)
		}
	}
	n := connection.Network()
	var err error
	// tcp
	if s.VirtualTcpListener, err = s.virtualBridgeListener(t.TCP, connection.KindBridgeTCP, report); err != nil {
		return err
	}
	// tls
	if s.VirtualTlsListener, err = s.virtualBridgeListener(t.TLS, connection.KindBridgeTLS, report); err != nil {
		return err
	}
	// ws
	if s.VirtualWsListener, err = s.virtualBridgeListener(t.WS, connection.KindBridgeWS, report); err != nil {
		return err
	}
	// wss
	if s.VirtualWssListener, err = s.virtualBridgeListener(t.WSS, connection.KindBridgeWSS, report); err != nil {
		return err
	}

	// kcp
	if t.KCP {
		logs.Info("Server start, the bridge type is kcp, the bridge port is %d", n.BridgeKCPPort)
		l, err := conn.ListenKCP(net.JoinHostPort(n.BridgeKCPIP, strconv.Itoa(n.BridgeKCPPort)))
		if err != nil {
			return fmt.Errorf("kcp bridge listener: %w", err)
		}
		s.addCloser(l)
		s.tasks.Go(func() {
			err := conn.ServeKCP(l, func(c net.Conn) {
				s.serve(conn.NewConn(c), conn.TransportKCP)
			})
			report(fmt.Errorf("kcp bridge listener: %w", err))
		})
	}

	// quic
	if t.QUIC {
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
		s.addCloser(l)
		s.tasks.Go(func() {
			err := conn.ServeQUIC(l, func(c net.Conn) {
				s.serve(conn.NewConn(c), conn.TransportQUIC)
			})
			report(fmt.Errorf("quic bridge listener: %w", err))
		})
	}

	s.tasks.Go(s.ping)
	s.acceptOn(s.VirtualTcpListener, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportTCP)
	})
	s.acceptOn(s.VirtualTlsListener, func(c net.Conn) {
		s.serve(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{crypt.GetCert()}})), conn.TransportTLS)
	})
	wsLn := conn.NewWSListener(s.VirtualWsListener, n.BridgePath, n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	s.addCloser(wsLn)
	s.acceptOn(wsLn, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportWS)
	})
	wssLn := conn.NewWSSListener(s.VirtualWssListener, n.BridgePath, crypt.GetCert(), n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	s.addCloser(wssLn)
	s.acceptOn(wssLn, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportWSS)
	})
	return nil
}

// acceptOn serves l in the background until it is closed.
func (s *Bridge) acceptOn(l net.Listener, handle func(net.Conn)) {
	s.tasks.Go(func() { _ = conn.Accept(l, handle) })
}

// virtualBridgeListener returns the in-memory listener of one bridge
// transport. When the transport is enabled it opens the real listener of
// kind k, reports its address and feeds it the connections that listener
// accepts, reporting to failed if that listener stops on an error; bridge://
// links dial the in-memory listener either way.
func (s *Bridge) virtualBridgeListener(enabled bool, k connection.Kind, failed func(error)) (*netx.ChanListener, error) {
	if !enabled {
		vl := conn.NewVirtualListener(nil)
		s.addCloser(vl)
		return vl, nil
	}
	ln, err := connection.Listen(k)
	if err != nil {
		return nil, err
	}
	s.addCloser(ln)
	vl := conn.NewVirtualListener(ln.Addr())
	s.addCloser(vl)
	s.tasks.Go(func() {
		if err := conn.Accept(ln, func(c net.Conn) { conn.ServeVirtual(vl, c) }); err != nil {
			failed(fmt.Errorf("%v listener: %w", k, err))
		}
	})
	return vl, nil
}

func (s *Bridge) addCloser(c io.Closer) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.ctx.Err() != nil {
		_ = c.Close()
		return
	}
	s.closers = append(s.closers, c)
}

// serve handles a connection a bridge listener accepted. Close waits for it
// and closes the connection if it is still open then.
func (s *Bridge) serve(c *conn.Conn, transport string) {
	if !s.tasks.Enter() {
		_ = c.Close()
		return
	}
	defer s.tasks.Leave()
	if !s.trackConn(c) {
		_ = c.Close()
		return
	}
	s.CliProcess(c, transport)
}

// trackConn has Close close c unless c is closed before.
func (s *Bridge) trackConn(c *conn.Conn) bool {
	s.closeMu.Lock()
	if s.ctx.Err() != nil {
		s.closeMu.Unlock()
		return false
	}
	s.conns[c] = struct{}{}
	s.closeMu.Unlock()
	c.OnClose(func(c *conn.Conn) {
		s.closeMu.Lock()
		delete(s.conns, c)
		s.closeMu.Unlock()
	})
	return true
}

// spawn runs f in a goroutine Close waits for; when the bridge is closing it
// closes c instead, which f would have served.
func (s *Bridge) spawn(c *conn.Conn, f func()) {
	if !s.tasks.Go(f) {
		_ = c.Close()
	}
}

// Close stops the bridge: it closes the listeners, disconnects the clients,
// closes the connections still being served, stops P2P and returns once
// every goroutine the bridge started has returned.
func (s *Bridge) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.tasks.Close()
		s.closeMu.Lock()
		closers := s.closers
		s.closers = nil
		conns := make([]*conn.Conn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.closeMu.Unlock()
		for _, c := range closers {
			_ = c.Close()
		}
		s.closeClients()
		for _, c := range conns {
			_ = c.Close()
		}
		s.StopP2P()
		s.tasks.Wait()
		// a handshake that ended while closing may have added a client
		s.closeClients()
	})
	return nil
}

// closeClients disconnects every client without the bookkeeping of a client
// that leaves: the server goes, not the client.
func (s *Bridge) closeClients() {
	s.Client.Range(func(key, value any) bool {
		s.Client.CompareAndDelete(key, value)
		if c, ok := value.(*Client); ok && c != nil {
			_ = c.Close()
		}
		return true
	})
}
