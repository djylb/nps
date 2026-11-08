package httpproxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
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
	f := &s.fallback
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return false
	}
	if f.srv == nil {
		f.ln = conn.NewVirtualListener(nil)
		f.srv = s.newFallbackServer()
		srv, ln := f.srv, f.ln
		go func() { _ = srv.Serve(ln) }()
	}
	ln := f.ln
	f.mu.Unlock()
	return ln.TryDeliver(&fallbackConn{Conn: c, state: state})
}

func (s *HttpServer) newFallbackServer() *http.Server {
	return &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = "http"
			if st, ok := r.Context().Value(tlsStateKey{}).(*tls.ConnectionState); ok && st != nil {
				r.TLS, r.URL.Scheme = st, "https"
			}
			s.handleProxy(w, r)
		}),
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

func (s *HttpServer) closeFallback() {
	f := &s.fallback
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = true
	if f.srv != nil {
		_ = f.srv.Close()
		_ = f.ln.Close()
	}
}
