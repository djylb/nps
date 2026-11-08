package httpproxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/slogx"
)

// fallbackServer runs the domain proxy over connections handed over from the
// bridge. It is an http.Server of its own: a connection replayed after a TLS
// termination elsewhere is not a *tls.Conn, so net/http cannot see its TLS,
// and the state comes with the connection instead.
type fallbackServer struct {
	mu   sync.Mutex
	ln   *netx.ChanListener
	srv  *http.Server
	done bool
}

// fallbackConn is a handed-over connection and the TLS state it ended.
type fallbackConn struct {
	net.Conn
	state *tls.ConnectionState
}

type tlsStateKey struct{}

// ServeFallback serves c through the domain proxy, every byte of the HTTP
// request still unread. state is the TLS c arrived over, nil for none: only
// HTTP/1.1 is possible, as no ALPN was negotiated for it. It returns false,
// leaving c to the caller, when the proxy has no HTTP engine or is closed.
func (s *HttpProxy) ServeFallback(c net.Conn, state *tls.ConnectionState) bool {
	var hs *HttpServer
	_ = s.BindLocked(func() error { hs = s.HttpServer; return nil })
	if hs == nil {
		return false
	}
	return hs.serveFallback(c, state)
}

func (s *HttpServer) serveFallback(c net.Conn, state *tls.ConnectionState) bool {
	return s.fallback.deliver(c, state, s.newFallbackServer)
}

// deliver hands c to the HTTP server of f, started on first use by newServer.
func (f *fallbackServer) deliver(c net.Conn, state *tls.ConnectionState, newServer func() *http.Server) bool {
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return false
	}
	if f.srv == nil {
		f.ln = conn.NewVirtualListener(nil)
		f.srv = newServer()
		srv, ln := f.srv, f.ln
		go func() { _ = srv.Serve(ln) }()
	}
	ln := f.ln
	f.mu.Unlock()
	return ln.TryDeliver(&fallbackConn{Conn: c, state: state})
}

func (f *fallbackServer) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = true
	if f.srv != nil {
		_ = f.srv.Close()
		_ = f.ln.Close()
	}
}

// fallbackServerFor returns the HTTP server of connections handed over from
// the bridge, answering with handler.
func fallbackServerFor(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if fc, ok := c.(*fallbackConn); ok {
				return context.WithValue(ctx, tlsStateKey{}, fc.state)
			}
			return ctx
		},
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		ErrorLog:          slogx.HTTPErrorLog(proxyLog, "fallback"),
	}
}

type fallbackKey struct{}

func (s *HttpServer) newFallbackServer() *http.Server {
	return fallbackServerFor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = "http"
		if st, ok := r.Context().Value(tlsStateKey{}).(*tls.ConnectionState); ok && st != nil {
			r.TLS, r.URL.Scheme = st, "https"
		}
		// Requests of this path answer an unknown host with the plain 404
		// below, whatever error_always says.
		s.handleProxy(w, r.WithContext(context.WithValue(r.Context(), fallbackKey{}, true)))
	}))
}

func isFallback(r *http.Request) bool {
	v, _ := r.Context().Value(fallbackKey{}).(bool)
	return v
}

// defaultNotFound is the body of the 404 of a connection that matches no
// route, when fallback_404_file names none. It says nothing of the software.
const defaultNotFound = "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n</body>\r\n</html>\r\n"

// LoadFallback404 returns the body of fallback_404_file, the built-in one if
// path is empty or unreadable.
func LoadFallback404(path string) []byte {
	if path != "" {
		b, err := os.ReadFile(runenv.ResolvePath(path))
		if err == nil {
			return b
		}
		proxyLog.Warn("fallback 404 file unreadable, using the built-in one", "file", path, "err", err)
	}
	return []byte(defaultNotFound)
}

func writeNotFound(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(body)
}

// NotFound answers every request of the connections handed to it with the
// 404 of an unknown site: the fallback of a node that runs no domain proxy.
type NotFound struct {
	body []byte
	f    fallbackServer
}

// NewNotFound returns a NotFound that answers with body.
func NewNotFound(body []byte) *NotFound { return &NotFound{body: body} }

// ServeFallback serves c, every byte of its request unread; it returns false
// once closed.
func (n *NotFound) ServeFallback(c net.Conn, state *tls.ConnectionState) bool {
	return n.f.deliver(c, state, func() *http.Server {
		return fallbackServerFor(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeNotFound(w, n.body) }))
	})
}

// Close stops serving.
func (n *NotFound) Close() error { n.f.close(); return nil }

// FallbackCert returns the certificate for the TLS of a bridge connection
// that asks for hello.ServerName, chosen as the domain proxy chooses for a
// visitor of that name, or nil, nil when the name matches no route and the
// caller's default certificate applies.
func (s *HttpProxy) FallbackCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var hs *HttpsServer
	_ = s.BindLocked(func() error { hs = s.HttpsServer; return nil })
	if hs == nil || hello.ServerName == "" {
		return nil, nil
	}
	return hs.fallbackCert(hello), nil
}

func (s *HttpsServer) fallbackCert(hello *tls.ClientHelloInfo) *tls.Certificate {
	sni := hello.ServerName
	host, err := s.store.FindCertByHost(sni)
	if err != nil || host == nil || host.HttpsJustProxy {
		// no route, or one whose TLS the backend ends, which a bridge
		// connection cannot pass through
		return nil
	}
	if s.autoSSL(host) {
		cert, err := s.certMagicTls.GetCertificate(hello)
		if err != nil {
			proxyLog.Debug("auto ssl certificate for a bridge connection failed", "route", host.Host, "domain", sni, "err", err)
			return nil
		}
		return cert
	}
	cert, err := s.selectCert(host, sni)
	if err != nil {
		return nil
	}
	return cert
}

func (s *HttpServer) closeFallback() { s.fallback.close() }
