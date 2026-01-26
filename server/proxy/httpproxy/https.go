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

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/cache"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type HttpsServer struct {
	*HttpServer
	httpsStatus        bool
	httpsListener      net.Listener
	httpsServeListener *HttpsListener
	httpsServer        *http.Server
	cert               *cache.CertManager
	certMagicTls       *tls.Config
	hasDefaultCert     bool
	defaultCertHash    string
	defaultCertFile    string
	defaultKeyFile     string
	ticketKeys         [][32]byte
	tlsNextProtos      []string
}

func NewHttpsServer(httpServer *HttpServer, l net.Listener) *HttpsServer {
	https := &HttpsServer{
		HttpServer:      httpServer,
		httpsStatus:     false,
		httpsListener:   l,
		defaultCertFile: beego.AppConfig.String("https_default_cert_file"),
		defaultKeyFile:  beego.AppConfig.String("https_default_key_file"),
	}

	_, https.hasDefaultCert = common.LoadCert(https.defaultCertFile, https.defaultKeyFile)
	https.defaultCertHash = crypt.FNV1a64("file", https.defaultCertFile, https.defaultKeyFile)

	maxNum := beego.AppConfig.DefaultInt("ssl_cache_max", 0)
	reload := beego.AppConfig.DefaultInt("ssl_cache_reload", 0)
	idle := beego.AppConfig.DefaultInt("ssl_cache_idle", 60)
	https.cert = cache.NewCertManager(maxNum, time.Duration(reload)*time.Second, time.Duration(idle)*time.Minute)
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

func (s *HttpsServer) Start() error {
	if s.httpsStatus {
		return errors.New("https server already started")
	}
	s.httpsStatus = true
	conn.Accept(s.httpsListener, func(c net.Conn) {
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
			s.handleHttpsProxy(host, c, rb, serverName)
			return
		}

		var tlsConfig *tls.Config
		if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl) {
			logs.Debug("Auto SSL is enabled")
			tlsConfig = s.certMagicTls
		} else {
			cert, err := s.cert.Get(host.CertFile, host.KeyFile, host.CertType, host.CertHash)
			if err != nil {
				if s.hasDefaultCert {
					cert, err = s.cert.Get(s.defaultCertFile, s.defaultKeyFile, "file", s.defaultCertHash)
					if err != nil {
						logs.Error("Failed to load certificate: %v", err)
					}
				}
				if err != nil {
					logs.Debug("Certificate handled by backend")
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
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return
		}
		if host.TlsOffload {
			s.handleTlsProxy(host, tlsConn, serverName)
			return
		}
		s.httpsServeListener.acceptConn <- tlsConn
	})
	s.httpsStatus = false
	return nil
}

func checkHTTPAndRedirect(c net.Conn, rb []byte) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.Close()

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
	_ = s.DealClient(conn.NewConn(c), host.Client, targetAddr, rb, common.CONN_TCP, nil, []*file.Flow{host.Flow, host.Client.Flow}, host.Target.ProxyProtocol, host.Target.LocalProxy, task)
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
	_ = s.DealClient(conn.NewConn(tlsConn), host.Client, targetAddr, nil, common.CONN_TCP, nil, []*file.Flow{host.Flow, host.Client.Flow}, host.Target.ProxyProtocol, host.Target.LocalProxy, task)
}

func (s *HttpsServer) Close() error {
	_ = s.httpsServer.Close()
	_ = s.httpsServeListener.Close()
	s.cert.Stop()
	s.httpsStatus = false
	return s.httpsListener.Close()
}

// HttpsListener wraps a parent listener.
type HttpsListener struct {
	acceptConn     chan *tls.Conn
	parentListener net.Listener
	closeOnce      sync.Once
}

func NewHttpsListener(l net.Listener) *HttpsListener {
	return &HttpsListener{
		parentListener: l,
		acceptConn:     make(chan *tls.Conn, 1024),
	}
}

func (l *HttpsListener) Accept() (net.Conn, error) {
	httpsConn, ok := <-l.acceptConn
	if !ok {
		return nil, errors.New("failed to get connection")
	}
	return httpsConn, nil
}

func (l *HttpsListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.acceptConn)
	})
	return nil
}

func (l *HttpsListener) Addr() net.Addr {
	return l.parentListener.Addr()
}
