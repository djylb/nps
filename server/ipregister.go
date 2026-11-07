package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/servercfg"
)

// SetIPRegisterHandler sets the registration page that Start serves; call it
// before Start. Without it the page does not run.
func (s *Server) SetIPRegisterHandler(handler http.Handler) { s.ipRegHandler = handler }

// startIPRegister serves the registration page to bridge://ipreg targets
// and, when the configuration sets ip_register_port, on a listener of its own.
func (s *Server) startIPRegister(cfg *servercfg.Snapshot) {
	if s.ipRegHandler == nil {
		return
	}
	hs := &http.Server{Handler: s.ipRegHandler, ErrorLog: obs.HTTPErrorLog(webLog, "ip_register")}
	if !s.own(hs) {
		return
	}
	virtual := conn.NewVirtualListener(nil)
	s.bridge.SetIPRegisterListener(virtual)
	s.goWork("ip register", func(context.Context) { _ = hs.Serve(virtual) })
	if cfg.App.IPRegisterPort <= 0 {
		return
	}
	var tlsConfig *tls.Config
	if cfg.App.IPRegisterCertFile != "" || cfg.App.IPRegisterKeyFile != "" {
		cert, err := loadCertPair("ip_register", cfg.App.IPRegisterCertFile, cfg.App.IPRegisterKeyFile)
		if err != nil {
			log.Error("ip registration page cert failed", "err", err)
			return
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	addr := net.JoinHostPort(cfg.App.IPRegisterIP, strconv.Itoa(cfg.App.IPRegisterPort))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		obs.Emit(log, &logevent.ListenFailed, "route", "ip_register", "addr", addr, "reason", logevent.NetReason(err), "err", err)
		return
	}
	scheme := "http"
	if tlsConfig != nil {
		l = tls.NewListener(l, tlsConfig)
		scheme = "https"
	}
	log.Info("ip registration page listening", "scheme", scheme, "addr", addr)
	if !s.goWork("ip register listener", func(context.Context) { _ = hs.Serve(l) }) {
		_ = l.Close()
	}
}
