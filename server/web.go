package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
)

type WebServer struct {
	proxy.BaseServer
	tcpListener net.Listener
}

type httpServeTarget struct {
	listener net.Listener
	serve    func(net.Listener) error
}

type webHandlerBox struct{ http.Handler }

// webHandler is the handler of the web manager, replaced on a config reload.
var webHandler atomic.Pointer[webHandlerBox]

func SetWebHandler(handler http.Handler) {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	webHandler.Store(&webHandlerBox{handler})
}

func serveWeb(w http.ResponseWriter, r *http.Request) {
	if h := webHandler.Load(); h != nil {
		h.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *WebServer) Start() error {
	ip := connection.Network().WebIP
	p := connection.Network().WebPort
	handler := http.HandlerFunc(serveWeb)
	cfg := servercfg.Current()

	var tlsConfig *tls.Config
	if p > 0 && cfg.Web.OpenSSL {
		cert, err := loadWebCert(cfg.Web.CertFile, cfg.Web.KeyFile)
		if err != nil {
			return err
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}

	if bridge.WebServerListener != nil {
		_ = bridge.WebServerListener.Close()
		bridge.WebServerListener = nil
	}
	lAddr := &net.TCPAddr{IP: net.ParseIP(ip), Port: p}
	bridge.WebServerListener = conn.NewVirtualListener(lAddr)
	targets := []httpServeTarget{{
		listener: bridge.WebServerListener,
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
	return loadCertPair("web", certFile, keyFile)
}

// loadCertPair loads the certificate and key configured as
// prefix_cert_file and prefix_key_file, given as paths or PEM content.
func loadCertPair(prefix, certFile, keyFile string) (tls.Certificate, error) {
	certContent, err := loadWebPEM(prefix+"_cert_file", certFile, "CERTIFICATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	keyContent, err := loadWebPEM(prefix+"_key_file", keyFile, "PRIVATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tlsconn.ParseKeyPair([]byte(certContent), []byte(keyContent))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("%s certificate: %w", prefix, err)
	}
	return cert, nil
}

func loadWebPEM(name, value, header string) (string, error) {
	content, err := crypt.GetCertContent(value, header)
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
	if bridge.WebServerListener != nil {
		_ = bridge.WebServerListener.Close()
		bridge.WebServerListener = nil
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
