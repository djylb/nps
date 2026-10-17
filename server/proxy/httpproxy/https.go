package httpproxy

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
)

const (
	// tlsHandshakeTimeout bounds reading the ClientHello on the HTTPS entry and
	// completing a TLS handshake with a locally available certificate.
	tlsHandshakeTimeout = 10 * time.Second
	// autoSSLHandshakeTimeout bounds a TLS handshake that may obtain a
	// certificate on demand from the ACME CA before it can finish.
	autoSSLHandshakeTimeout = 90 * time.Second
)

type HttpsServer struct {
	*HttpServer
	httpsListener      net.Listener
	httpsServeListener *HttpsListener
	httpsServer        *http.Server
	cert               *tlsconn.CertFiles
	certMagicTls       *tls.Config
	defaultCertFile    string
	defaultKeyFile     string
	ticketKeys         [][32]byte
	tlsNextProtos      []string
	httpsStatus        bool
	hasDefaultCert     bool
}

func NewHttpsServer(httpServer *HttpServer, l net.Listener) *HttpsServer {
	cfg := servercfg.Current()
	https := &HttpsServer{
		HttpServer:      httpServer,
		httpsStatus:     false,
		httpsListener:   l,
		defaultCertFile: cfg.Proxy.SSL.DefaultCertFile,
		defaultKeyFile:  cfg.Proxy.SSL.DefaultKeyFile,
	}

	_, https.hasDefaultCert = common.LoadCert(https.defaultCertFile, https.defaultKeyFile)
	https.cert = newCertFiles(cfg.Proxy.SSL)
	https.httpsServeListener = NewHttpsListener(l)
	https.httpsServer = https.NewServer(https.HttpsPort, "https")

	https.tlsNextProtos = []string{"h2", "http/1.1"}
	if https.Http3Port != 0 {
		https.tlsNextProtos = append([]string{"h3"}, https.tlsNextProtos...)
	}

	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		logs.Error("failed to generate session ticket key: %v", err)
		s := crypt.GetRandomString(len(key))
		copy(key[:], s)
	}
	https.ticketKeys = append(https.ticketKeys, key)

	https.certMagicTls = https.Magic.TLSConfig()
	https.certMagicTls.NextProtos = append(https.tlsNextProtos, https.certMagicTls.NextProtos...)
	https.certMagicTls.SetSessionTicketKeys(https.ticketKeys)

	go func() {
		if err := https.httpsServer.Serve(https.httpsServeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.Error("HTTPS server exit: %v", err)
		}
	}()

	return https
}

// newCertFiles returns the cache of the certificates of the hosts, sized and
// timed by the ssl_cache settings: ssl_cache_reload is how often, in
// seconds, the files are checked for changes, never if zero.
func newCertFiles(cfg servercfg.SSLConfig) *tlsconn.CertFiles {
	check := time.Duration(cfg.CacheReload) * time.Second
	if check <= 0 {
		check = -1
	}
	return &tlsconn.CertFiles{
		MaxEntries:    cfg.CacheMax,
		IdleTimeout:   time.Duration(cfg.CacheIdle) * time.Minute,
		CheckInterval: check,
		OnError: func(err *tlsconn.CertError) {
			logs.Warn("Failed to reload certificate, the previous one stays in use: %v", err)
		},
	}
}

// loadCert returns the certificate of certFile and keyFile: of the files,
// relative ones in the run directory, if certType is "file", or of the PEM
// data they hold if it is "text".
func (s *HttpsServer) loadCert(certFile, keyFile, certType string) (*tls.Certificate, error) {
	switch certType {
	case "file":
		return s.cert.LoadFiles(common.GetPath(certFile), common.GetPath(keyFile))
	case "text":
		return s.cert.LoadPEM([]byte(certFile), []byte(keyFile))
	default:
		return nil, errors.New("cert must be 'file' or 'text'")
	}
}

func (s *HttpsServer) Start() error {
	if s.httpsStatus {
		return errors.New("https server already started")
	}
	s.httpsStatus = true
	conn.Accept(s.httpsListener, func(c net.Conn) {
		_ = c.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
		helloInfo, rb, err := crypt.ReadClientHello(c, nil)
		if err != nil || helloInfo == nil {
			logs.Debug("Failed to read clientHello from %v, err=%v", c.RemoteAddr(), err)
			// Check if the request is an HTTP request.
			checkHTTPAndRedirect(c, rb)
			return
		}

		serverName := helloInfo.ServerName
		if serverName == "" {
			logs.Debug("IP access to HTTPS port is not allowed. Remote address: %v", c.RemoteAddr())
			_ = c.Close()
			return
		}

		host, err := file.GetDb().FindCertByHost(serverName)
		if err != nil || host.IsClose {
			_ = c.Close()
			logs.Debug("The URL %s cannot be parsed! Remote address: %v", serverName, c.RemoteAddr())
			return
		}

		if host.HttpsJustProxy {
			logs.Debug("Certificate handled by backend")
			_ = c.SetDeadline(time.Time{})
			s.handleHttpsProxy(host, c, rb, serverName)
			return
		}

		var tlsConfig *tls.Config
		handshakeTimeout := tlsHandshakeTimeout
		if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl) {
			logs.Debug("Auto SSL is enabled")
			tlsConfig = s.certMagicTls
			handshakeTimeout = autoSSLHandshakeTimeout
		} else {
			cert, err := s.loadCert(host.CertFile, host.KeyFile, host.CertType)
			if err != nil {
				if s.hasDefaultCert {
					cert, err = s.loadCert(s.defaultCertFile, s.defaultKeyFile, "file")
					if err != nil {
						logs.Error("Failed to load certificate: %v", err)
					}
				}
				if err != nil {
					logs.Debug("Certificate handled by backend")
					_ = c.SetDeadline(time.Time{})
					s.handleHttpsProxy(host, c, rb, serverName)
					return
				}
			}
			tlsConfig = &tls.Config{
				Certificates: []tls.Certificate{*cert},
			}
			tlsConfig.NextProtos = s.tlsNextProtos
			tlsConfig.SetSessionTicketKeys(s.ticketKeys)
		}

		acceptConn := conn.NewConn(c).SetRb(rb)
		tlsConn := tls.Server(acceptConn, tlsConfig)
		_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return
		}
		_ = c.SetDeadline(time.Time{})
		if host.TlsOffload {
			s.handleTlsProxy(host, tlsConn, serverName)
			return
		}
		s.httpsServeListener.push(tlsConn)
	})
	s.httpsStatus = false
	return nil
}

func checkHTTPAndRedirect(c net.Conn, rb []byte) {
	_ = c.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
	defer func() { _ = c.Close() }()

	logs.Debug("Pre-read rb content: %q", string(rb))

	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(rb), c))
	req, err := http.ReadRequest(reader)
	if err != nil {
		logs.Debug("Failed to parse HTTP request from %v, err=%v", c.RemoteAddr(), err)
		return
	}
	logs.Debug("HTTP Request Sent to HTTPS Port")
	req.URL.Scheme = "https"
	_ = c.SetDeadline(time.Time{})

	_, err = file.GetDb().GetInfoByHost(req.Host, req)
	if err != nil {
		logs.Debug("Host not found: %s %s %s", req.URL.Scheme, req.Host, req.RequestURI)
		return
	}

	redirectURL := "https://" + req.Host + req.RequestURI

	response := "HTTP/1.1 302 Found\r\n" +
		"Location: " + redirectURL + "\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n\r\n"

	if _, writeErr := c.Write([]byte(response)); writeErr != nil {
		logs.Error("Failed to write redirect response to %v, err=%v", c.RemoteAddr(), writeErr)
	} else {
		logs.Info("Redirected HTTP request from %v to %s", c.RemoteAddr(), redirectURL)
	}
}

func (s *HttpsServer) handleHttpsProxy(host *file.Host, c net.Conn, rb []byte, sni string) {
	if err := s.CheckFlowAndConnNum(host.Client); err != nil {
		logs.Debug("Client id %d, host id %d, error %v during https connection", host.Client.Id, host.Id, err)
		_ = c.Close()
		return
	}
	defer host.Client.CutConn()
	host.AddConn()
	defer host.CutConn()

	targetAddr, err := host.Target.GetRandomTarget()
	if err != nil {
		logs.Warn("%v", err)
		_ = c.Close()
		return
	}
	logs.Info("New HTTPS connection, clientId %d, host %s, remote address %v", host.Client.Id, sni, c.RemoteAddr())
	task := file.NewTunnelByHost(host, s.HttpsPort)
	// DealClient owns the connection and closes it.
	//noinspection GoResourceLeak
	_ = s.DealClient(conn.NewConn(c), host.Client, targetAddr, rb, common.ConnTCP, nil, []*file.Flow{host.Flow, host.Client.Flow}, host.Target.ProxyProtocol, host.Target.LocalProxy, task)
}

func (s *HttpsServer) handleTlsProxy(host *file.Host, tlsConn net.Conn, sni string) {
	if err := s.CheckFlowAndConnNum(host.Client); err != nil {
		logs.Debug("Client id %d, host id %d, TLS-only check failed: %v", host.Client.Id, host.Id, err)
		_ = tlsConn.Close()
		return
	}
	defer host.Client.CutConn()
	host.AddConn()
	defer host.CutConn()

	targetAddr, err := host.Target.GetRandomTarget()
	if err != nil {
		logs.Warn("%v", err)
		_ = tlsConn.Close()
		return
	}
	logs.Info("New TLS offload connection, clientId %d, host %s, remote %v -> %s", host.Client.Id, sni, tlsConn.RemoteAddr(), targetAddr)
	task := file.NewTunnelByHost(host, s.HttpsPort)
	// DealClient owns the connection and closes it.
	//noinspection GoResourceLeak
	_ = s.DealClient(conn.NewConn(tlsConn), host.Client, targetAddr, nil, common.ConnTCP, nil, []*file.Flow{host.Flow, host.Client.Flow}, host.Target.ProxyProtocol, host.Target.LocalProxy, task)
}

func (s *HttpsServer) Close() error {
	_ = s.httpsServer.Close()
	_ = s.httpsServeListener.Close()
	s.cert.Clear()
	s.httpsStatus = false
	return s.httpsListener.Close()
}

// HttpsListener wraps a parent listener and hands out connections whose TLS
// handshake has already completed.
type HttpsListener struct {
	acceptConn     chan *tls.Conn
	done           chan struct{}
	parentListener net.Listener
	closeOnce      sync.Once
}

func NewHttpsListener(l net.Listener) *HttpsListener {
	return &HttpsListener{
		parentListener: l,
		acceptConn:     make(chan *tls.Conn),
		done:           make(chan struct{}),
	}
}

// push hands c to Accept, or closes it once the listener is closed.
func (l *HttpsListener) push(c *tls.Conn) {
	select {
	case l.acceptConn <- c:
	case <-l.done:
		_ = c.Close()
	}
}

func (l *HttpsListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.acceptConn:
		select {
		case <-l.done:
			_ = c.Close()
			return nil, net.ErrClosed
		default:
			return c, nil
		}
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *HttpsListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
	})
	return nil
}

func (l *HttpsListener) Addr() net.Addr {
	return l.parentListener.Addr()
}
