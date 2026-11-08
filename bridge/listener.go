package bridge

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
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
// closes the bridge: Done is closed then and Cause tells which listener
// failed.
func (s *Bridge) StartTunnel(t Transports) error {
	select {
	case <-s.scope.Done():
		return ErrClosed
	default:
	}
	n := connection.Network()
	var err error
	// tcp
	if s.VirtualTcpListener, err = s.virtualBridgeListener(t.TCP, connection.KindBridgeTCP); err != nil {
		return err
	}
	// tls
	if s.VirtualTlsListener, err = s.virtualBridgeListener(t.TLS, connection.KindBridgeTLS); err != nil {
		return err
	}
	// ws
	if s.VirtualWsListener, err = s.virtualBridgeListener(t.WS, connection.KindBridgeWS); err != nil {
		return err
	}
	// wss
	if s.VirtualWssListener, err = s.virtualBridgeListener(t.WSS, connection.KindBridgeWSS); err != nil {
		return err
	}

	// kcp
	if t.KCP {
		l, err := conn.ListenKCP(net.JoinHostPort(n.BridgeKCPIP, strconv.Itoa(n.BridgeKCPPort)))
		if err != nil {
			return fmt.Errorf("kcp bridge listener: %w", err)
		}
		slogx.Emit(context.Background(), log, &logevent.Listening, "route", "bridge", "listen", l.Addr().String(), "carrier", "kcp")
		s.addCloser(l)
		_ = s.scope.Go("kcp bridge listener", func(context.Context) error {
			return conn.ServeKCP(l, func(c net.Conn) {
				s.serve(conn.NewConn(c), conn.TransportKCP)
			})
		})
	}

	// quic
	if t.QUIC {
		quicConfig := &quic.Config{
			KeepAlivePeriod:    time.Duration(n.QUICKeepAlivePeriod) * time.Second,
			MaxIdleTimeout:     time.Duration(n.QUICMaxIdleTimeout) * time.Second,
			MaxIncomingStreams: n.QUICMaxIncomingStreams,
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{s.cert},
			NextProtos:   n.QUICALPNList,
		}
		l, err := conn.ListenQUIC(net.JoinHostPort(n.BridgeQUICIP, strconv.Itoa(n.BridgeQUICPort)), tlsCfg, quicConfig)
		if err != nil {
			return fmt.Errorf("quic bridge listener: %w", err)
		}
		slogx.Emit(context.Background(), log, &logevent.Listening, "route", "bridge", "listen", l.Addr().String(), "carrier", "quic")
		s.addCloser(l)
		_ = s.scope.Go("quic bridge listener", func(context.Context) error {
			return conn.ServeQUIC(l, func(c net.Conn) {
				s.serve(conn.NewConn(c), conn.TransportQUIC)
			})
		})
	}

	_ = s.scope.Go("ping", func(context.Context) error { s.ping(); return nil })
	s.acceptOn(s.VirtualTcpListener, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportTCP)
	})
	s.acceptOn(s.VirtualTlsListener, func(c net.Conn) {
		s.serve(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})), conn.TransportTLS)
	})
	wsLn := conn.NewWSListener(s.VirtualWsListener, n.BridgePath, n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	s.addCloser(wsLn)
	s.acceptOn(wsLn, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportWS)
	})
	wssLn := conn.NewWSSListener(s.VirtualWssListener, n.BridgePath, s.cert, n.BridgeTrustedIPs, n.BridgeRealIPHeader)
	s.addCloser(wssLn)
	s.acceptOn(wssLn, func(c net.Conn) {
		s.serve(conn.NewConn(c), conn.TransportWSS)
	})
	return nil
}

// acceptOn serves l in the background until it is closed.
func (s *Bridge) acceptOn(l net.Listener, handle func(net.Conn)) {
	_ = s.scope.Go("accept", func(context.Context) error {
		_ = conn.Accept(l, handle)
		return nil
	})
}

// virtualBridgeListener returns the in-memory listener of one bridge
// transport. When the transport is enabled it opens the real listener of
// kind k, reports its address and feeds it the connections that listener
// accepts; that listener stopping on an error closes the bridge. bridge://
// links dial the in-memory listener either way.
func (s *Bridge) virtualBridgeListener(enabled bool, k connection.Kind) (*netx.ChanListener, error) {
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
	_ = s.scope.Go(fmt.Sprintf("%v listener", k), func(context.Context) error {
		return conn.Accept(ln, func(c net.Conn) { conn.ServeVirtual(vl, c) })
	})
	return vl, nil
}

// addCloser has Close close c, or closes c when the bridge is closed.
func (s *Bridge) addCloser(c io.Closer) { _, _ = s.scope.Add(c) }

// anyError reports every error as expected: once the bridge closes, the
// listeners and handlers may end with any error.
func anyError(error) bool { return true }

// serve handles a connection a bridge listener accepted. Close waits for it
// and closes the connection if it is still open then; once the bridge is
// closed it closes the connection at once.
func (s *Bridge) serve(c *conn.Conn, transport string) {
	s.spawn(c, func() {
		remove, err := s.conns.Add(c)
		if err != nil {
			return // closed by Add
		}
		c.OnClose(func(*conn.Conn) { remove() })
		s.CliProcess(c, transport)
	})
}

// spawn runs f in a goroutine Close waits for; when the bridge is closing it
// closes c instead, which f would have served.
func (s *Bridge) spawn(c *conn.Conn, f func()) {
	if s.conns.Go("conn", func(context.Context) error { f(); return nil }) != nil {
		_ = c.Close()
	}
}

// Done is closed when the bridge starts closing: by Close, or because one of
// its listeners failed.
func (s *Bridge) Done() <-chan struct{} { return s.scope.Done() }

// Cause returns why the bridge closed: the failure of a listener, or
// lifecycle.ErrClosed after Close; nil while it is open.
func (s *Bridge) Cause() error { return s.scope.Cause() }

// Close stops the bridge: it closes the listeners and the connections still
// being served, disconnects the clients, stops P2P and returns once every
// goroutine the bridge started has returned.
func (s *Bridge) Close() error {
	s.closeOnce.Do(func() {
		s.scope.Close(nil)
		s.closeClients()
		s.StopP2P()
		_ = s.scope.Wait(context.Background())
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
