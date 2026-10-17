package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
	"github.com/djylb/nps/server/tool"
)

type WebServer struct {
	proxy.BaseServer
	tcpListener net.Listener
}

type httpServeTarget struct {
	listener net.Listener
	serve    func(net.Listener) error
}

var webHandler = http.NotFoundHandler()

func SetWebHandler(handler http.Handler) {
	if handler == nil {
		webHandler = http.NotFoundHandler()
		return
	}
	webHandler = handler
}

func (s *WebServer) Start() error {
	ip := connection.WebIp
	p := connection.WebPort
	handler := webHandler
	cfg := servercfg.Current()

	var tlsConfig *tls.Config
	if p > 0 && cfg.Web.OpenSSL {
		cert, err := loadWebCert(cfg.Web.CertFile, cfg.Web.KeyFile)
		if err != nil {
			return err
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}

	if tool.WebServerListener != nil {
		_ = tool.WebServerListener.Close()
		tool.WebServerListener = nil
	}
	lAddr := &net.TCPAddr{IP: net.ParseIP(ip), Port: p}
	tool.WebServerListener = conn.NewVirtualListener(lAddr)
	targets := []httpServeTarget{{
		listener: tool.WebServerListener,
		serve: func(l net.Listener) error {
			return http.Serve(l, handler)
		},
	}}

	if p > 0 {
		if l, err := connection.GetWebManagerListener(); err == nil {
			s.tcpListener = l
			if tlsConfig != nil {
				targets = append(targets, httpServeTarget{
					listener: l,
					serve: func(l net.Listener) error {
						srv := &http.Server{Handler: handler, TLSConfig: tlsConfig}
						return srv.ServeTLS(l, "", "")
					},
				})
			} else {
				targets = append(targets, httpServeTarget{
					listener: l,
					serve: func(l net.Listener) error {
						return http.Serve(l, handler)
					},
				})
			}
		} else {
			logs.Error("%v", err)
		}
	} else {
		logs.Info("web_port=0: only virtual listener is active (plain HTTP)")
	}

	return serveHTTPListeners(targets...)
}

// loadWebCert loads the web manager certificate. Each value may be inline PEM,
// an absolute path, or a path relative to the run directory.
func loadWebCert(certFile, keyFile string) (tls.Certificate, error) {
	certContent, err := loadWebPEM("web_cert_file", certFile, "CERTIFICATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	keyContent, err := loadWebPEM("web_key_file", keyFile, "PRIVATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair([]byte(certContent), []byte(keyContent))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("web certificate: %w", err)
	}
	return cert, nil
}

func loadWebPEM(name, value, header string) (string, error) {
	content, err := common.GetCertContent(value, header)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", name, value, err)
	}
	if content == "" {
		return "", fmt.Errorf("%s %q: no PEM %s block found", name, value, header)
	}
	return content, nil
}

func (s *WebServer) Close() error {
	if s.tcpListener != nil {
		_ = s.tcpListener.Close()
	}
	if tool.WebServerListener != nil {
		_ = tool.WebServerListener.Close()
		tool.WebServerListener = nil
	}
	return nil
}

func NewWebServer(bridge *bridge.Bridge) *WebServer {
	s := new(WebServer)
	s.Bridge = bridge
	return s
}

func serveHTTPListeners(targets ...httpServeTarget) error {
	errCh := make(chan error, 1)
	var once sync.Once

	closeAll := func() {
		for _, target := range targets {
			if target.listener != nil {
				_ = target.listener.Close()
			}
		}
	}

	for _, target := range targets {
		if target.listener == nil || target.serve == nil {
			continue
		}
		go func(target httpServeTarget) {
			err := target.serve(target.listener)
			once.Do(func() {
				closeAll()
				errCh <- err
			})
		}(target)
	}

	return <-errCh
}
