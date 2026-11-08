package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/portmux/tlshello"
	"github.com/djylb/slogx"
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
	httpsStatus        atomic.Bool
	hasDefaultCert     bool
	hostCerts          sync.Map // host id → *tls.Certificate last reported loaded
}

func NewHttpsServer(httpServer *HttpServer, l net.Listener) *HttpsServer {
	cfg := servercfg.Current()
	https := &HttpsServer{
		HttpServer:      httpServer,
		httpsListener:   l,
		defaultCertFile: cfg.Proxy.SSL.DefaultCertFile,
		defaultKeyFile:  cfg.Proxy.SSL.DefaultKeyFile,
	}

	https.cert = newCertFiles(cfg.Proxy.SSL)
	if https.defaultCertFile != "" {
		cert, err := https.loadCert(https.defaultCertFile, https.defaultKeyFile, "file")
		https.hasDefaultCert = err == nil
		if err != nil {
			slogx.Emit(context.Background(), proxyLog, &logevent.CertFailed, "route", "https", "file", https.defaultCertFile, "reason", "load_failed", "err", err)
		} else {
			LogCertLoaded("https", "", https.defaultCertFile, cert)
		}
	}
	https.httpsServeListener = NewHttpsListener(l)
	https.httpsServer = https.NewServer(https.HttpsPort, "https")

	https.tlsNextProtos = []string{"h2", "http/1.1"}
	if https.Http3Port != 0 {
		https.tlsNextProtos = append([]string{"h3"}, https.tlsNextProtos...)
	}

	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		proxyLog.Error("session ticket key generation failed", "err", err)
		s := crypt.GetRandomString(len(key))
		copy(key[:], s)
	}
	https.ticketKeys = append(https.ticketKeys, key)

	https.certMagicTls = https.Magic.TLSConfig()
	https.certMagicTls.NextProtos = append(https.tlsNextProtos, https.certMagicTls.NextProtos...)
	https.certMagicTls.SetSessionTicketKeys(https.ticketKeys)

	go func() {
		if err := https.httpsServer.Serve(https.httpsServeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			proxyLog.Error("https server stopped", "err", err)
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
		MaxEntries:    max(cfg.CacheMax, 0),
		IdleTimeout:   time.Duration(cfg.CacheIdle) * time.Minute,
		CheckInterval: check,
		OnError: func(err *tlsconn.CertError) {
			slogx.Emit(context.Background(), proxyLog, &logevent.CertFailed, slogx.KeyMsg, "certificate reload failed, the previous one stays in use", "reason", "load_failed", "err", err)
		},
	}
}

// loadCert returns the certificate of certFile and keyFile: of the files,
// relative ones in the run directory, if certType is "file", or of the PEM
// data they hold if it is "text".
func (s *HttpsServer) loadCert(certFile, keyFile, certType string) (*tls.Certificate, error) {
	switch certType {
	case "file":
		return s.cert.LoadFiles(runenv.GetPath(certFile), runenv.GetPath(keyFile))
	case "text":
		return s.cert.LoadPEM([]byte(certFile), []byte(keyFile))
	default:
		return nil, errors.New("cert must be 'file' or 'text'")
	}
}

func (s *HttpsServer) Start() error {
	if !s.httpsStatus.CompareAndSwap(false, true) {
		return errors.New("https server already started")
	}
	conn.Accept(s.httpsListener, func(c net.Conn) {
		// Only the global list: the entry lists depend on the request's host,
		// which may differ from the SNI.
		if proxy.SourceDenied(c.RemoteAddr(), s.Bridge.GlobalSource(), nil, nil, "") {
			_ = c.Close()
			return
		}
		_ = c.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
		hello, rb, err := readClientHello(c)
		if err != nil {
			proxyLog.Debug("client hello read failed", "src_ip", srcIP(c.RemoteAddr().String()), "err", err)
			// Check if the request is an HTTP request.
			s.checkHTTPAndRedirect(c, rb)
			return
		}

		serverName := hello.ServerName
		if serverName == "" {
			proxyLog.Debug("https request without sni dropped", "src_ip", srcIP(c.RemoteAddr().String()))
			_ = c.Close()
			return
		}

		host, err := s.store.FindCertByHost(serverName)
		if err != nil {
			host = nil
		}

		if host != nil && host.HttpsJustProxy {
			proxyLog.Debug("certificate handled by backend", "route", host.Host, "host", host.Id)
			_ = c.SetDeadline(time.Time{})
			s.handleHttpsProxy(host, c, rb, serverName)
			return
		}

		tlsConfig, autoSSL := s.serverTLSConfig(host, serverName)
		switch {
		case tlsConfig != nil:
		case host == nil:
			_ = c.Close()
			proxyLog.Debug("no host for sni", "src_ip", srcIP(c.RemoteAddr().String()), "route", "https", "domain", serverName)
			return
		default:
			proxyLog.Debug("certificate handled by backend", "route", host.Host, "host", host.Id)
			_ = c.SetDeadline(time.Time{})
			s.handleHttpsProxy(host, c, rb, serverName)
			return
		}
		handshakeTimeout := tlsHandshakeTimeout
		if autoSSL {
			proxyLog.Debug("auto ssl handshake", "route", host.Host, "host", host.Id, "domain", serverName)
			handshakeTimeout = autoSSLHandshakeTimeout
		}

		acceptConn := netx.NewPrefixConn(c, rb)
		tlsConn := tls.Server(acceptConn, tlsConfig)
		_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return
		}
		_ = c.SetDeadline(time.Time{})
		if host != nil && host.TlsOffload {
			s.handleTlsProxy(host, tlsConn, serverName)
			return
		}
		s.httpsServeListener.push(tlsConn)
	})
	s.httpsStatus.Store(false)
	return nil
}

// maxClientHelloBytes bounds the bytes read while looking for a complete
// ClientHello.
const maxClientHelloBytes = 64 * 1024

// readClientHello reads from c until it holds a complete TLS ClientHello and
// returns it with every byte read, which the caller replays. On failure the
// bytes read so far are returned as well, such as a plain HTTP request.
func readClientHello(c net.Conn) (*tlshello.Hello, []byte, error) {
	var s tlshello.Scanner
	data := make([]byte, 0, 2048)
	for {
		if len(data) == cap(data) {
			data = slices.Grow(data, cap(data))
		}
		n, err := c.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if n > 0 {
			hello, perr := s.Scan(data)
			if perr == nil {
				return hello, data, nil
			}
			if !errors.Is(perr, io.ErrUnexpectedEOF) {
				return nil, data, perr
			}
			if len(data) >= maxClientHelloBytes {
				return nil, data, tlshello.ErrInvalid
			}
		}
		if err != nil {
			return nil, data, err
		}
	}
}

func (s *HttpsServer) checkHTTPAndRedirect(c net.Conn, rb []byte) {
	_ = c.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
	defer func() { _ = c.Close() }()

	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(rb), c))
	req, err := http.ReadRequest(reader)
	if err != nil {
		proxyLog.Debug("not an http request on https port", "src_ip", srcIP(c.RemoteAddr().String()), "bytes", len(rb), "err", err)
		return
	}
	req.URL.Scheme = "https"
	_ = c.SetDeadline(time.Time{})
	req.RemoteAddr = c.RemoteAddr().String()
	start := time.Now()
	recorded := s.accessEnabled(req.Context())
	a := &accessState{route: "https"}

	host, err := s.store.GetInfoByHost(req.Host, req)
	if err != nil {
		if recorded {
			s.writeAccess(req, a, 0, 0, start)
		}
		return
	}
	a.route, a.host = host.Host, host.Id

	redirectURL := "https://" + req.Host + req.RequestURI

	response := "HTTP/1.1 302 Found\r\n" +
		"Location: " + redirectURL + "\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n\r\n"

	status := http.StatusFound
	if _, writeErr := c.Write([]byte(response)); writeErr != nil {
		proxyLog.Debug("redirect write failed", "src_ip", srcIP(req.RemoteAddr), "route", host.Host, "err", writeErr)
		status = 0
	}
	if recorded {
		s.writeAccess(req, a, status, 0, start)
	}
}

func (s *HttpsServer) handleHttpsProxy(host *model.Host, c net.Conn, rb []byte, sni string) {
	release, err := s.AcquireForwarding(host.Client, host.ControlGroup())
	if err != nil {
		emitLimit(host, err)
		_ = c.Close()
		return
	}
	defer release()

	proxyLog.Debug("https passthrough connection", "route", host.Host, "host", host.Id, "client", host.Client.Id, "domain", sni, "src_ip", srcIP(c.RemoteAddr().String()))
	task := model.NewTunnelByHost(host, s.HttpsPort)
	// DealTarget owns the connection once it found a target.
	//noinspection GoResourceLeak
	if err := s.DealTarget(conn.NewConn(c), host.Client, rb, conn.LinkTCP, nil, []conn.Flow{host.Flow, host.Client.Flow}, task); errors.Is(err, model.ErrNoTarget) {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", host.Host, "host", host.Id, "client", host.Client.Id, "reason", "no_target", "err", err)
		_ = c.Close()
	}
}

func (s *HttpsServer) handleTlsProxy(host *model.Host, tlsConn net.Conn, sni string) {
	release, err := s.AcquireForwarding(host.Client, host.ControlGroup())
	if err != nil {
		emitLimit(host, err)
		_ = tlsConn.Close()
		return
	}
	defer release()

	proxyLog.Debug("tls offload connection", "route", host.Host, "host", host.Id, "client", host.Client.Id, "domain", sni, "src_ip", srcIP(tlsConn.RemoteAddr().String()))
	task := model.NewTunnelByHost(host, s.HttpsPort)
	// DealTarget owns the connection once it found a target.
	//noinspection GoResourceLeak
	if err := s.DealTarget(conn.NewConn(tlsConn), host.Client, nil, conn.LinkTCP, nil, []conn.Flow{host.Flow, host.Client.Flow}, task); errors.Is(err, model.ErrNoTarget) {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", host.Host, "host", host.Id, "client", host.Client.Id, "reason", "no_target", "err", err)
		_ = tlsConn.Close()
	}
}

func (s *HttpsServer) Close() error {
	_ = s.httpsServer.Close()
	_ = s.httpsServeListener.Close()
	s.cert.Clear()
	s.httpsStatus.Store(false)
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
