package server

import (
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/tool"
)

// StartIPRegisterServer serves handler, the registration page, to
// bridge://ipreg targets and, when the configuration sets ip_register_port,
// on a listener of its own.
func StartIPRegisterServer(handler http.Handler) {
	tool.IPRegisterListener = conn.NewVirtualListener(nil)
	go func() { _ = http.Serve(tool.IPRegisterListener, handler) }()
	cfg := servercfg.Current()
	if cfg.App.IPRegisterPort <= 0 {
		return
	}
	var tlsConfig *tls.Config
	if cfg.App.IPRegisterCertFile != "" || cfg.App.IPRegisterKeyFile != "" {
		cert, err := loadCertPair("ip_register", cfg.App.IPRegisterCertFile, cfg.App.IPRegisterKeyFile)
		if err != nil {
			logs.Error("IP registration page: %v", err)
			return
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	addr := common.BuildAddress(cfg.App.IPRegisterIP, strconv.Itoa(cfg.App.IPRegisterPort))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		logs.Error("IP registration page: %v", err)
		return
	}
	scheme := "http"
	if tlsConfig != nil {
		l = tls.NewListener(l, tlsConfig)
		scheme = "https"
	}
	logs.Info("IP registration page listening on %s://%s", scheme, addr)
	go func() { _ = http.Serve(l, handler) }()
}
