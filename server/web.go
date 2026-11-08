package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/netx"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/slogx"
)

// WebServer serves the web manager of a Server on the web port and on the
// in-memory listener that bridge://web targets reach.
type WebServer struct {
	srv         *Server
	mu          sync.Mutex
	closed      bool
	tcpListener net.Listener
	virtual     *netx.ChanListener
	servers     []*http.Server // closing them closes their connections too
}

type httpServeTarget struct {
	listener net.Listener
	serve    func(net.Listener) error
}

type webHandlerBox struct{ http.Handler }

// SetWebHandler sets the handler of the web manager; it applies to a running
// server, so a config reload replaces it.
func (s *Server) SetWebHandler(handler http.Handler) {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	s.webHandler.Store(&webHandlerBox{handler})
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if h := s.webHandler.Load(); h != nil {
		h.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *WebServer) Start() error {
	ip := connection.Network().WebIP
	p := connection.Network().WebPort
	handler := http.HandlerFunc(s.srv.serveWeb)
	cfg := servercfg.Current()

	var tlsConfig *tls.Config
	if p > 0 && cfg.Web.OpenSSL {
		cert, err := loadWebCert(cfg.Web.CertFile, cfg.Web.KeyFile)
		if err != nil {
			return err
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}

	lAddr := &net.TCPAddr{IP: net.ParseIP(ip), Port: p}
	virtual := conn.NewVirtualListener(lAddr)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = virtual.Close()
		return net.ErrClosed
	}
	plain := &http.Server{Handler: handler, ErrorLog: slogx.HTTPErrorLog(webLog, "web")}
	s.virtual = virtual
	s.servers = append(s.servers, plain)
	s.mu.Unlock()
	s.srv.bridge.SetWebListener(virtual)
	targets := []httpServeTarget{{
		listener: virtual,
		serve:    plain.Serve,
	}}

	if p > 0 {
		l, err := connection.Listen(connection.KindWeb)
		if err != nil {
			_ = s.Close()
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = l.Close()
			return net.ErrClosed
		}
		s.tcpListener = l
		var secure *http.Server
		if tlsConfig != nil {
			secure = &http.Server{Handler: handler, TLSConfig: tlsConfig, ErrorLog: slogx.HTTPErrorLog(webLog, "web_tls")}
			s.servers = append(s.servers, secure)
		}
		s.mu.Unlock()
		if secure != nil {
			targets = append(targets, httpServeTarget{
				listener: l,
				serve: func(l net.Listener) error {
					return secure.ServeTLS(l, "", "")
				},
			})
		} else {
			targets = append(targets, httpServeTarget{
				listener: l,
				serve:    plain.Serve,
			})
		}
	} else {
		webLog.Info("only virtual listener active", "reason", "web_port is 0")
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

// Close closes the listeners of the web manager; Start then returns.
func (s *WebServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.tcpListener != nil {
		_ = s.tcpListener.Close()
	}
	if s.virtual != nil {
		s.srv.bridge.SetWebListener(nil)
		_ = s.virtual.Close()
	}
	for _, hs := range s.servers {
		_ = hs.Close()
	}
	return nil
}

// NewWebServer returns the web manager of srv.
func NewWebServer(srv *Server) *WebServer {
	return &WebServer{srv: srv}
}

// serveHTTPListeners serves the targets until one of them stops, closes the
// others and returns the first error once all of them have returned.
func serveHTTPListeners(targets ...httpServeTarget) error {
	errCh := make(chan error, 1)
	var once sync.Once
	var wg sync.WaitGroup

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
		wg.Go(func() {
			err := target.serve(target.listener)
			once.Do(func() {
				closeAll()
				errCh <- err
			})
		})
	}
	wg.Wait()
	return <-errCh
}

var webLog = slogx.Module("web")
