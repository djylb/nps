package conn

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/forwarded"
	"github.com/djylb/netx/wsconn"
	"github.com/djylb/netx/wsconn/wshttp"
	"github.com/djylb/nps/lib/common"
)

// wsOptions closes without the closing handshake, so that closing a tunnel
// never waits on the peer.
var wsOptions = wsconn.Options{DisableCloseHandshake: true}

type httpListener struct {
	acceptCh  chan net.Conn
	closeCh   chan struct{}
	addr      net.Addr
	closeOnce sync.Once
}

// NewWSListener serves WebSocket bridge connections on path over base. The
// client address is taken from realIpHeader as written by the proxies in
// trustedIps; see wsResolver.
func NewWSListener(base net.Listener, path, trustedIps, realIpHeader string) net.Listener {
	return newWSListener(base, base, path, trustedIps, realIpHeader)
}

// NewWSSListener is NewWSListener over TLS with cert.
func NewWSSListener(base net.Listener, path string, cert tls.Certificate, trustedIps, realIpHeader string) net.Listener {
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	return newWSListener(base, tls.NewListener(base, tlsConfig), path, trustedIps, realIpHeader)
}

func newWSListener(base, ln net.Listener, path, trustedIps, realIpHeader string) net.Listener {
	hl := &httpListener{acceptCh: make(chan net.Conn, 16), closeCh: make(chan struct{}), addr: base.Addr()}
	resolvers := wsResolvers(trustedIps, realIpHeader)
	opts := &wsconn.ServerOptions{Options: wsOptions, CheckOrigin: wsconn.AnyOrigin}
	if path == "" {
		path = "/"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hl.closeCh:
			return
		default:
		}
		ws, err := wshttp.Upgrade(w, r, opts)
		if err != nil {
			return
		}
		var c net.Conn = wsconn.NewStreamConn(ws)
		if addr := resolveClient(resolvers, r); addr != nil {
			c = netx.NewAddrOverrideConn(c, nil, addr)
		}
		select {
		case hl.acceptCh <- c:
		case <-hl.closeCh:
			_ = c.Close()
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: HandshakeTimeout}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-hl.closeCh
		_ = srv.Close()
	}()
	return hl
}

// wsResolvers returns the resolvers tried in turn for the client address:
// the header realIpHeader names, or X-Forwarded-For and then X-Real-IP when
// it is empty. They return nothing without trusted proxies.
func wsResolvers(trustedIps, realIpHeader string) []*forwarded.Resolver {
	if trustedIps == "" {
		return nil
	}
	trusted := common.TrustedProxies(trustedIps)
	if realIpHeader == "" {
		return []*forwarded.Resolver{
			{Trusted: trusted, Header: forwarded.HeaderXForwardedFor},
			{Trusted: trusted, Header: forwarded.HeaderXRealIP},
		}
	}
	switch name := http.CanonicalHeaderKey(realIpHeader); name {
	case "X-Forwarded-For":
		return []*forwarded.Resolver{{Trusted: trusted, Header: forwarded.HeaderXForwardedFor}}
	case "Forwarded":
		return []*forwarded.Resolver{{Trusted: trusted, Header: forwarded.HeaderForwarded}}
	default:
		return []*forwarded.Resolver{{Trusted: trusted, Header: forwarded.HeaderXRealIP, Name: name}}
	}
}

// resolveClient returns the client address that a trusted proxy recorded
// for r, or nil when r came from the client itself.
func resolveClient(resolvers []*forwarded.Resolver, r *http.Request) net.Addr {
	peer, ok := forwarded.ParseNode(r.RemoteAddr)
	if !ok {
		return nil
	}
	for _, res := range resolvers {
		if c := res.Resolve(peer, r.Header); c.Hops > 0 {
			return net.TCPAddrFromAddrPort(c.Addr)
		}
	}
	return nil
}

func (hl *httpListener) Accept() (net.Conn, error) {
	select {
	case c := <-hl.acceptCh:
		return c, nil
	case <-hl.closeCh:
		return nil, net.ErrClosed
	}
}

func (hl *httpListener) Close() error {
	hl.closeOnce.Do(func() {
		close(hl.closeCh)
	})
	return nil
}

func (hl *httpListener) Addr() net.Addr {
	return hl.addr
}

// DialWS runs the WebSocket opening handshake for urlStr over rawConn, which
// already carries TLS for a wss URL, sending host as the Host field when it
// is set, and returns the byte stream over the connection's messages. On
// failure rawConn is closed.
func DialWS(rawConn net.Conn, urlStr, host string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ws, _, err := wsconn.Client(ctx, rawConn, urlStr, &wsconn.ClientOptions{Options: wsOptions, Host: host})
	if err != nil {
		return nil, err
	}
	return wsconn.NewStreamConn(ws), nil
}
