package httpproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type Http3Server struct {
	*HttpsServer
	http3Status     atomic.Bool
	http3Listener   net.PacketConn
	http3Server     *http3.Server
	http3NextProtos []string

	// workers counts the accept loop of the bridge mode and its QUIC
	// connection handlers; Close waits for them.
	workers sync.WaitGroup
	closeMu sync.Mutex
	closing bool
}

func NewHttp3Server(httpsSrv *HttpsServer, udpConn net.PacketConn) *Http3Server {
	return &Http3Server{
		HttpsServer:   httpsSrv,
		http3Listener: udpConn,
	}
}

func (s *Http3Server) Start() error {
	if s.http3Status.Load() {
		return errors.New("http3 server is already running")
	}
	s.http3Status.Store(true)
	s.http3NextProtos = []string{http3.NextProtoH3}
	tlsConfig := &tls.Config{
		NextProtos:         s.http3NextProtos,
		GetConfigForClient: s.GetConfigForClient,
	}
	tlsConfig.SetSessionTicketKeys(s.ticketKeys)

	if !s.Http3Bridge {
		srv := &http3.Server{
			Handler:   s.httpsServer.Handler,
			TLSConfig: tlsConfig,
		}
		s.setServer(srv)

		if err := srv.Serve(s.http3Listener); err != nil {
			logs.Error("HTTP/3 Serve error: %v", err)
			s.http3Status.Store(false)
			return err
		}
		s.http3Status.Store(false)
		return nil
	}

	s.http3NextProtos = append([]string{http3.NextProtoH3}, connection.Network().QUICALPNList...)
	quicConfig := &quic.Config{
		KeepAlivePeriod:    time.Duration(connection.Network().QUICKeepAlivePeriod) * time.Second,
		MaxIdleTimeout:     time.Duration(connection.Network().QUICMaxIdleTimeout) * time.Second,
		MaxIncomingStreams: connection.Network().QUICMaxIncomingStreams,
		Allow0RTT:          true,
	}

	if !s.addWorker() {
		s.http3Status.Store(false)
		return nil
	}
	defer s.workers.Done()
	tr := &quic.Transport{Conn: s.http3Listener}
	defer func() { _ = tr.Close() }()
	ln, err := tr.ListenEarly(tlsConfig, quicConfig)
	if err != nil {
		s.http3Status.Store(false)
		return err
	}
	s.setServer(&http3.Server{
		Handler: s.httpsServer.Handler,
	})

	ctx := context.Background()
	for {
		qc, err := ln.Accept(ctx)
		if err != nil {
			logs.Trace("HTTP/3 Accept transient error: %v", err)
			if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, quic.ErrServerClosed) || errors.Is(err, quic.ErrTransportClosed) {
				break
			}
			continue
		}
		state := qc.ConnectionState()
		alpn := state.TLS.NegotiatedProtocol
		sni := state.TLS.ServerName
		if !s.addWorker() {
			_ = qc.CloseWithError(0, "server closed")
			continue
		}
		go func() {
			defer s.workers.Done()
			s.HandleQUIC(qc, alpn, sni)
		}()
	}
	_ = ln.Close()
	s.http3Status.Store(false)
	return nil
}

func (s *Http3Server) setServer(srv *http3.Server) {
	s.closeMu.Lock()
	s.http3Server = srv
	s.closeMu.Unlock()
}

// addWorker counts a new worker unless Close has begun.
func (s *Http3Server) addWorker() bool {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closing {
		return false
	}
	s.workers.Add(1)
	return true
}

// Close stops the server. Closing the socket ends the QUIC transport and its
// connections, so the workers return.
func (s *Http3Server) Close() error {
	s.closeMu.Lock()
	s.closing = true
	srv := s.http3Server
	s.closeMu.Unlock()
	defer s.workers.Wait()
	if srv != nil {
		_ = srv.Close()
	}
	s.http3Status.Store(false)
	return s.http3Listener.Close()
}

func (s *Http3Server) HandleQUIC(qc *quic.Conn, alpn, sni string) {
	if alpn == http3.NextProtoH3 && !strings.EqualFold(sni, connection.Network().BridgeHost) {
		_ = s.http3Server.ServeQUICConn(qc)
		return
	}
	s.serveBridgeQUIC(qc)
}

func (s *Http3Server) serveBridgeQUIC(qc *quic.Conn) {
	stream, err := qc.AcceptStream(context.Background())
	if err != nil {
		logs.Trace("QUIC accept stream error: %v", err)
		_ = qc.CloseWithError(0, "closed")
		return
	}
	c := conn.NewQuicAutoCloseConn(stream, qc)
	// CliProcess owns the connection.
	//noinspection GoResourceLeak
	s.Bridge.CliProcess(conn.NewConn(c), "quic")
}

func (s *Http3Server) GetConfigForClient(info *tls.ClientHelloInfo) (*tls.Config, error) {
	host, err := file.GetDb().FindCertByHost(info.ServerName)
	if err != nil {
		host = nil
	}
	// TLS offload hosts forward the decrypted stream as raw TCP, which an
	// HTTP/3 server cannot provide.
	if host != nil && (host.HttpsJustProxy || host.TlsOffload) {
		return nil, nil
	}

	config, autoSSL := s.serverTLSConfig(host, info.ServerName)
	if config == nil || autoSSL {
		return config, nil
	}
	if s.Http3Bridge {
		config.NextProtos = s.http3NextProtos
	}

	return config, nil
}
