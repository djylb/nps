package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
)

// coreHooks connects the new protocol entry to the bridge: a session is a
// node of its client, beside the nodes of the old protocol, and links reach
// it through the same selection (Node.path).
func (s *Bridge) coreHooks() coreapi.Hooks {
	return coreapi.Hooks{
		Online:     s.coreOnline,
		Takeover:   coreTakeover,
		Offline:    s.coreOffline,
		AuthFailed: coreAuthFailed,
		SuspendChanged: func(cs coreapi.Session) {
			s.coreChanged(cs.ClientID())
		},
		Legacy: func(c net.Conn, carrier string) {
			s.serve(conn.NewConn(c), carrier)
		},
		Fallback:     s.coreFallback,
		ResolveLink:  s.coreResolveLink,
		Coordinate:   s.coreCoordinate,
		ConfigUpload: s.coreConfigUpload,
	}
}

// coreNodeUUID names the node of a core session: one npc process.
func coreNodeUUID(cs coreapi.Session) string { return hex.EncodeToString([]byte(cs.Instance())) }

// agentVersion is the version in Hello's agent, "npc/1.2.3 linux/amd64".
func agentVersion(agent string) string {
	first, _, _ := strings.Cut(agent, " ")
	if _, v, ok := strings.Cut(first, "/"); ok {
		return v
	}
	return agent
}

// coreOnline registers a session as the old protocol registers a connection:
// the client's address and carrier, then joinNode, which does the presence.
func (s *Bridge) coreOnline(cs coreapi.Session) {
	id := cs.ClientID()
	if c, err := s.store.GetClient(id); err == nil {
		c.Lock()
		c.Addr, c.LocalAddr, c.Mode = cs.Source().Addr().Unmap().String(), "", cs.Carrier()
		c.Unlock()
	}
	uuid := coreNodeUUID(cs)
	node := NewNode(uuid, agentVersion(cs.Agent()), legacy.LatestIndex)
	node.AddCore(cs)
	s.joinNode(id, uuid, node, func(n *Node) { n.AddCore(cs) })
	s.coreChanged(id)
}

// coreChanged wakes what waits for a session of client id (awaitPath).
func (s *Bridge) coreChanged(id int) {
	if v, ok := s.Client.Load(id); ok {
		v.(*Client).notifyChange()
	}
}

func coreTakeover(cs, old coreapi.Session) {
	slogx.Emit(context.Background(), log, &logevent.SessionTakeover, "client", cs.ClientID(), "src_ip", cs.Source().Addr().Unmap().String(), "addr", old.Source().String())
}

// coreOffline drops the node of a session that ended, unless a newer session
// of the same npc holds it now. A client that may no longer connect leaves at
// once; otherwise it has the grace of the health checks, as on the old
// protocol.
func (s *Bridge) coreOffline(cs coreapi.Session, reason string) {
	id := cs.ClientID()
	if v, ok := s.Client.Load(id); ok {
		client := v.(*Client)
		if n, ok := client.GetNodeByUUID(coreNodeUUID(cs)); ok && n.CloseIfCoreCurrent(cs) {
			client.RemoveOfflineNodes(false)
		}
		client.notifyChange()
	}
	if reason == "deleted" {
		s.delClient(id, "deleted")
	}
}

func coreAuthFailed(f coreapi.AuthFailure) {
	if f.Reason == "pipes" {
		// A configured limit, not a failed authentication: fail2ban reads
		// auth_failed.
		slogx.Emit(context.Background(), log, &logevent.LimitExceeded, "client", f.Client, "route", "bridge", "kind", "pipes")
		return
	}
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
	// ServeHTTP3 serves qc as HTTP/3 through the domain proxy until it
	// ends. It returns false when there is no HTTP/3 engine.
	ServeHTTP3(qc *quic.Conn) bool
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
	denied := s.deniedHTTP
	notFound := denied(http.HandlerFunc(s.serveHTTPFallback))
	route := &conn.WSRoute{NotFound: notFound, Wrap: func(legacy http.Handler) http.Handler {
		// A request that is no WebSocket on the bridge path is a visitor's.
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				legacy.ServeHTTP(w, r)
				return
			}
			notFound.ServeHTTP(w, r)
		})
		h, err := s.core.WebSocket(next)
		if err != nil {
			log.Error("new protocol websocket route", "err", err)
			return legacy
		}
		return denied(h)
	}}
	for _, p := range connection.Network().NewProtocolWSPaths {
		h, err := s.core.WebSocketAnyProtocol(notFound)
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

// deniedHTTP drops a request whose address the global lists deny.
func (s *Bridge) deniedHTTP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if addr, err := conn.ParseTCPAddr(r.RemoteAddr); err == nil && addr != nil && s.store.GlobalDenied(addr, "bridge") {
			panic(http.ErrAbortHandler)
		}
		h.ServeHTTP(w, r)
	})
}

// serveHTTPFallback gives a request of a WebSocket bridge port that is not a
// carrier to the domain proxy, as the other bridge ports give theirs (§13):
// the connection, its parsed request written back in front.
func (s *Bridge) serveHTTPFallback(w http.ResponseWriter, r *http.Request) {
	if s.fallback.Load() == nil {
		http.NotFound(w, r)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	head := requestHead(r)
	if n := rw.Reader.Buffered(); n > 0 {
		b, _ := rw.Reader.Peek(n)
		head = append(head, b...)
	}
	// ServeFallback owns the connection when it takes it.
	//noinspection GoResourceLeak
	if !s.coreFallback(netx.NewPrefixConn(c, head), r.TLS) {
		_ = c.Close()
	}
}

// requestHead writes the head of a parsed request back: only its order of
// fields differs from what the client sent.
func requestHead(r *http.Request) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s %s\r\nHost: %s\r\n", r.Method, r.RequestURI, r.Proto, r.Host)
	if len(r.TransferEncoding) > 0 {
		fmt.Fprintf(&b, "Transfer-Encoding: %s\r\n", strings.Join(r.TransferEncoding, ","))
	}
	_ = r.Header.Write(&b)
	b.WriteString("\r\n")
	return b.Bytes()
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
