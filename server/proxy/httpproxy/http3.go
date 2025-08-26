package httpproxy

import (
	"crypto/tls"
	"errors"
	"net"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/quic-go/quic-go/http3"
)

type Http3Server struct {
	*HttpsServer
	http3Status   bool
	http3Listener net.PacketConn
	http3Server   *http3.Server
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
	tlsConfig := &tls.Config{
		NextProtos: []string{"h3"},
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			host, err := file.GetDb().FindCertByHost(info.ServerName)
			if err != nil || host.HttpsJustProxy || host.IsClose {
				return nil, nil
			}

			if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443) {
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

			return config, nil
		},
	}
	tlsConfig.SetSessionTicketKeys(s.ticketKeys)

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

func (s *Http3Server) Close() error {
	_ = s.http3Server.Close()
	s.httpsStatus = false
	return s.http3Listener.Close()
}
