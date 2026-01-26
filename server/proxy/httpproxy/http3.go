package httpproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
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
	http3Status     bool
	http3Listener   net.PacketConn
	http3Server     *http3.Server
	http3NextProtos []string
}

func NewHttp3Server(httpsSrv *HttpsServer, udpConn net.PacketConn) *Http3Server {
	return &Http3Server{
		http3Status:   false,
		HttpsServer:   httpsSrv,
		http3Listener: udpConn,
	}
}

func (s *Http3Server) Start() error {
	if s.http3Status {
		return errors.New("http3 server is already running")
	}
	s.httpStatus = true
	s.http3NextProtos = []string{http3.NextProtoH3}
	tlsConfig := &tls.Config{
		NextProtos:         s.http3NextProtos,
		GetConfigForClient: s.GetConfigForClient,
	}
	tlsConfig.SetSessionTicketKeys(s.ticketKeys)

	if !s.Http3Bridge {
		s.http3Server = &http3.Server{
			Handler:   s.httpsServer.Handler,
			TLSConfig: tlsConfig,
		}

		if err := s.http3Server.Serve(s.http3Listener); err != nil {
			logs.Error("HTTP/3 Serve error: %v", err)
			s.httpsStatus = false
			return err
		}
		s.httpsStatus = false
		return nil
	}

	s.http3NextProtos = append([]string{http3.NextProtoH3}, connection.QuicAlpn...)
	quicConfig := &quic.Config{
		KeepAlivePeriod:    time.Duration(connection.QuicKeepAliveSec) * time.Second,
		MaxIdleTimeout:     time.Duration(connection.QuicIdleTimeoutSec) * time.Second,
		MaxIncomingStreams: connection.QuicMaxStreams,
		Allow0RTT:          true,
	}

	tr := &quic.Transport{Conn: s.http3Listener}
	ln, err := tr.ListenEarly(tlsConfig, quicConfig)
	if err != nil {
		s.httpsStatus = false
		return err
	}
	s.http3Server = &http3.Server{
		Handler: s.httpsServer.Handler,
	}

	ctx := context.Background()
	for {
		qc, err := ln.Accept(ctx)
		if err != nil {
			logs.Trace("HTTP/3 Accept transient error: %v", err)
			if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, quic.ErrServerClosed) {
				break
			}
			continue
		}
		state := qc.ConnectionState()
		alpn := state.TLS.NegotiatedProtocol
		sni := state.TLS.ServerName
		go s.HandleQUIC(qc, alpn, sni)
	}
	_ = ln.Close()
	s.httpsStatus = false
	return nil
}

func (s *Http3Server) Close() error {
	_ = s.http3Server.Close()
	s.httpsStatus = false
	return s.http3Listener.Close()
}

func (s *Http3Server) HandleQUIC(qc *quic.Conn, alpn, sni string) {
	if alpn == http3.NextProtoH3 && !strings.EqualFold(sni, connection.BridgeHost) {
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
	s.Bridge.CliProcess(conn.NewConn(c), "quic")
}

func (s *Http3Server) GetConfigForClient(info *tls.ClientHelloInfo) (*tls.Config, error) {
	host, err := file.GetDb().FindCertByHost(info.ServerName)
	if err != nil || host.HttpsJustProxy || host.IsClose {
		return nil, nil
	}

	if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl) {
		return s.certMagicTls, nil
	}

	cert, err := s.cert.Get(host.CertFile, host.KeyFile, host.CertType, host.CertHash)
	if err != nil {
		if s.hasDefaultCert {
			cert, err = s.cert.Get(s.defaultCertFile, s.defaultKeyFile, "file", s.defaultCertHash)
			if err != nil {
				logs.Error("Failed to load certificate: %v", err)
			}
		}
		if err != nil {
			return nil, nil
		}
	}
	config := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	config.NextProtos = s.tlsNextProtos
	config.SetSessionTicketKeys(s.ticketKeys)

	if s.Http3Bridge {
		config.NextProtos = s.http3NextProtos
	}

	return config, nil
}
