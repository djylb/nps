package bridge

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/slogx"
)

// coreHooks connects the new protocol entry to the bridge. Its sessions carry
// no tunnels yet; step 2 of the switch plugs them in here.
func (s *Bridge) coreHooks() coreapi.Hooks {
	return coreapi.Hooks{
		Online:     coreOnline,
		Takeover:   coreTakeover,
		Offline:    coreOffline,
		AuthFailed: coreAuthFailed,
		Legacy: func(c net.Conn, carrier string) {
			s.serve(conn.NewConn(c), carrier)
		},
		Fallback: s.coreFallback,
	}
}

func coreOnline(cs coreapi.Session) {
	slogx.Emit(context.Background(), log, &logevent.ClientOnline, "client", cs.ClientID(), "src_ip", cs.Source().Addr().Unmap().String(), "carrier", cs.Carrier(), "version", cs.Agent())
}

func coreTakeover(cs, old coreapi.Session) {
	slogx.Emit(context.Background(), log, &logevent.SessionTakeover, "client", cs.ClientID(), "src_ip", cs.Source().Addr().Unmap().String(), "addr", old.Source().String())
}

func coreOffline(cs coreapi.Session, reason string) {
	slogx.Emit(context.Background(), log, &logevent.ClientOffline, "client", cs.ClientID(), "src_ip", cs.Source().Addr().Unmap().String(),
		"carrier", cs.Carrier(), "version", cs.Agent(), "dur_ms", time.Since(cs.Since()).Milliseconds(), "reason", reason)
}

func coreAuthFailed(f coreapi.AuthFailure) {
	args := []any{"src_ip", f.Source.Addr().Unmap().String(), "src_port", int(f.Source.Port()), "role", "client", "client", f.Client}
	if f.Carrier != "" {
		args = append(args, "carrier", f.Carrier)
	}
	args = append(args, "reason", f.Reason)
	if f.Flag != "" {
		args = append(args, "flag", f.Flag)
	}
	slogx.Emit(context.Background(), log, &logevent.AuthFailed, args...)
}

// Fallback is the target of the carriers the new protocol does not take (the
// domain proxy).
type Fallback interface {
	// ServeFallback serves c, every byte replayed; state is its TLS when
	// this process terminated it. It returns false, leaving c to the
	// caller, when it cannot take c.
	ServeFallback(c net.Conn, state *tls.ConnectionState) bool
	// FallbackCert returns the certificate for the TLS handshake of a
	// connection that asked for hello.ServerName, or nil, nil to present the
	// bridge's own.
	FallbackCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

func (s *Bridge) coreFallback(c net.Conn, state *tls.ConnectionState) bool {
	if f := s.fallback.Load(); f != nil {
		return (*f).ServeFallback(c, state)
	}
	return false
}

// SetFallback sets the target of carriers the new protocol does not take; nil
// removes it.
func (s *Bridge) SetFallback(f Fallback) {
	if f == nil {
		s.fallback.Store(nil)
		return
	}
	s.fallback.Store(&f)
}

// fallbackCert picks the certificate of the TLS bridge by the name the client
// asked for, as the domain proxy does, and the bridge's own when no route
// has that name.
func (s *Bridge) fallbackCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if f := s.fallback.Load(); f != nil {
		return (*f).FallbackCert(hello)
	}
	return nil, nil
}

// startCore starts the new protocol entry, before any listener opens.
func (s *Bridge) startCore() error {
	if s.core == nil {
		return nil
	}
	if err := s.core.Start(s.scope.Context(), s.coreHooks()); err != nil {
		return err
	}
	s.addCloser(s.core)
	return nil
}

// serveCarrier handles an accepted tcp or tls connection of a bridge port: with
// the new protocol on, admission routes it, and the old protocol's first bytes
// come back through Hooks.Legacy.
func (s *Bridge) serveCarrier(c net.Conn, transport string) {
	if s.core == nil {
		s.serve(conn.NewConn(c), transport)
		return
	}
	if tc, ok := c.(*net.TCPConn); ok {
		// the old protocol set this on its signal connection, which it
		// can no longer see behind the replayed prefix
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(5 * time.Second)
	}
	if s.store.GlobalDenied(c.RemoteAddr(), "bridge") {
		_ = c.Close()
		return
	}
	if s.conns.Go("core carrier", func(ctx context.Context) error {
		s.core.Serve(ctx, c, transport)
		return nil
	}) != nil {
		_ = c.Close()
	}
}

// coreWS is the route of the WebSocket bridge: on the bridge path the
// subprotocol picks the protocol, and new_protocol_ws_paths belong to the new
// one whatever the subprotocol. Nil with the new protocol off.
func (s *Bridge) coreWS() *conn.WSRoute {
	if s.core == nil {
		return nil
	}
	denied := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if addr, err := conn.ParseTCPAddr(r.RemoteAddr); err == nil && addr != nil && s.store.GlobalDenied(addr, "bridge") {
				panic(http.ErrAbortHandler)
			}
			h.ServeHTTP(w, r)
		})
	}
	route := &conn.WSRoute{Wrap: func(legacy http.Handler) http.Handler {
		h, err := s.core.WebSocket(legacy)
		if err != nil {
			log.Error("new protocol websocket route", "err", err)
			return legacy
		}
		return denied(h)
	}}
	for _, p := range connection.Network().NewProtocolWSPaths {
		h, err := s.core.WebSocketAnyProtocol(http.NotFoundHandler())
		if err != nil {
			log.Error("new protocol websocket route", "path", p, "err", err)
			continue
		}
		if route.Paths == nil {
			route.Paths = map[string]http.Handler{}
		}
		route.Paths[p] = denied(h)
	}
	return route
}

// tlsConfig is the configuration of the TLS bridge listener; the ALPN list
// and the choice of the certificate by name apply only with the new protocol
// (no ALPN by default, §13).
func (s *Bridge) tlsConfig() *tls.Config {
	cfg := &tls.Config{Certificates: []tls.Certificate{s.cert}}
	if s.core != nil {
		cfg.NextProtos = connection.Network().NewProtocolTLSALPN
		cfg.GetCertificate = s.fallbackCert
	}
	return cfg
}
