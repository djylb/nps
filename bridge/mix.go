package bridge

import (
	"context"
	"crypto/tls"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
)

// mixKind is an entry point that takes several carriers on one port. They
// share serveMix and mixOther and differ only in the rules of mixEntry.
type mixKind int

const (
	mixPort    mixKind = iota // mix_port
	mixVirtual                // bridge://mix
	mixGeneric                // a config with only bridge_port
)

// mixEntry is one mixed entry point.
type mixEntry struct {
	kind   mixKind
	plain  bool // plaintext carriers (tcp, ws)
	legacy bool // the old protocol, where the first bytes tell it
	// The HTTP engines of its connections, without and with TLS terminated
	// here: WebSocket carriers by the route of the entry, the rest to the
	// domain proxy.
	httpPlain, httpTLS *netx.ChanListener
}

func (s *Bridge) newMixEntry(kind mixKind) *mixEntry {
	n := connection.Network()
	e := &mixEntry{kind: kind}
	switch kind {
	case mixPort:
		e.plain, e.legacy = n.MixPlain, n.MixLegacy
	case mixVirtual:
		e.plain, e.legacy = true, n.MixLegacy
	case mixGeneric:
		e.plain, e.legacy = true, true
	}
	serve := func(transport string) *netx.ChanListener {
		vl := conn.NewVirtualListener(nil)
		s.addCloser(vl)
		ws := conn.NewWSListener(vl, n.BridgePath, n.BridgeTrustedIPs, n.BridgeRealIPHeader, s.mixRoute(kind))
		s.addCloser(ws)
		s.acceptOn(ws, func(c net.Conn) { s.serve(conn.NewConn(c), transport) })
		return vl
	}
	e.httpPlain, e.httpTLS = serve(conn.TransportWS), serve(conn.TransportWSS)
	return e
}

// mixRoute is the WebSocket routing of an entry. On a mixed port and
// bridge://mix a WebSocket is the new protocol whatever its subprotocol,
// since nothing tells the old one apart. The generic port keeps the rules of
// a single WebSocket bridge port, where the subprotocol decides (§12); with
// the new protocol off it serves only the old protocol's WebSocket, and any
// other request is dropped as that port always did.
func (s *Bridge) mixRoute(kind mixKind) *conn.WSRoute {
	if kind == mixGeneric {
		if r := s.coreWS(); r != nil {
			return r
		}
		abort := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
		return &conn.WSRoute{NotFound: abort, Wrap: func(old http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !isWebSocket(r) {
					abort(w, r)
					return
				}
				old.ServeHTTP(w, r)
			})
		}}
	}
	n := connection.Network()
	notFound := s.deniedHTTP(http.HandlerFunc(s.serveHTTPFallback))
	route := &conn.WSRoute{NotFound: notFound, Paths: map[string]http.Handler{}}
	path := n.BridgePath
	if path == "" {
		path = "/"
	}
	for _, p := range append(slices.Clone(n.NewProtocolWSPaths), path) {
		route.Paths[p] = notFound
		if s.core == nil {
			continue
		}
		h, err := s.core.WebSocketAnyProtocol(notFound)
		if err != nil {
			log.Error("new protocol websocket route", "path", p, "err", err)
			continue
		}
		route.Paths[p] = s.deniedHTTP(h)
	}
	return route
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// serveMixPort opens the listener of kind and serves it as e.
func (s *Bridge) serveMixPort(kind connection.Kind, e *mixEntry) error {
	ln, err := connection.Listen(kind)
	if err != nil {
		return err
	}
	s.addCloser(ln)
	_ = s.scope.Go(kind.String(), func(context.Context) error {
		return conn.Accept(ln, func(c net.Conn) { s.serveMix(c, e) })
	})
	return nil
}

// serveMix handles an accepted connection of a mixed entry point.
func (s *Bridge) serveMix(c net.Conn, e *mixEntry) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(5 * time.Second)
	}
	if s.store.GlobalDenied(c.RemoteAddr(), "bridge") {
		_ = c.Close()
		return
	}
	if s.conns.Go("mix carrier", func(ctx context.Context) error {
		s.dispatchMix(ctx, c, e)
		return nil
	}) != nil {
		_ = c.Close()
	}
}

// readHead reads up to n first bytes of c, within the handshake timeout, and
// returns them with c that replays them.
func readHead(c net.Conn, n int) ([]byte, net.Conn) {
	head := make([]byte, n)
	_ = c.SetReadDeadline(time.Now().Add(conn.HandshakeTimeout))
	got, _ := io.ReadFull(c, head)
	_ = c.SetReadDeadline(time.Time{})
	head = head[:got]
	return head, netx.NewPrefixConn(c, head)
}

// dispatchMix is the first step of §13: TLS by its first byte, terminated
// here; plaintext as the entry's rules allow.
func (s *Bridge) dispatchMix(ctx context.Context, c net.Conn, e *mixEntry) {
	// Closing the bridge ends what is still being dispatched.
	remove, err := s.conns.Add(c)
	if err != nil {
		return
	}
	defer remove()
	head, pc := readHead(c, 1)
	if len(head) == 0 {
		_ = c.Close()
		return
	}
	if head[0] == 0x16 {
		cfg := s.tlsConfig()
		cfg.GetCertificate = s.fallbackCert
		s.admitMix(ctx, tls.Server(pc, cfg), conn.TransportTLS, e)
		return
	}
	if e.plain {
		s.admitMix(ctx, pc, conn.TransportTCP, e)
		return
	}
	magic, nc := readHead(pc, len(legacy.Magic))
	if connection.IsHTTP(magic) && s.coreFallback(nc, nil) {
		return
	}
	s.stall(ctx, nc)
}

// admitMix gives a carrier to the new protocol's admission, or, with the
// new protocol off, directly to what comes after it.
func (s *Bridge) admitMix(ctx context.Context, c net.Conn, carrier string, e *mixEntry) {
	other := func(c net.Conn, st *tls.ConnectionState) bool { return s.mixOther(c, st, e) }
	if s.core != nil {
		s.core.ServeMixed(ctx, c, carrier, other)
		return
	}
	var st *tls.ConnectionState
	if tc, ok := c.(*tls.Conn); ok {
		hctx, cancel := context.WithTimeout(ctx, conn.HandshakeTimeout)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			_ = c.Close()
			return
		}
		state := tc.ConnectionState()
		st = &state
	}
	if !other(c, st) {
		s.stall(ctx, c)
	}
}

// mixOther routes what the new protocol's admission did not take: the old
// protocol's magic, HTTP, and on the generic port with the new protocol off
// the old handshake for anything else, as that port always did. It owns c
// when it returns true.
func (s *Bridge) mixOther(c net.Conn, st *tls.ConnectionState, e *mixEntry) bool {
	head, nc := readHead(c, len(legacy.Magic))
	transport := conn.TransportTCP
	if st != nil {
		transport = conn.TransportTLS
	}
	switch {
	case string(head) == legacy.Magic:
		if !e.legacy {
			return false
		}
		s.serve(conn.NewConn(nc), transport)
		return true
	case connection.IsHTTP(head):
		if st != nil {
			return e.httpTLS.TryDeliver(&conn.TLSStateConn{Conn: nc, State: st})
		}
		return e.httpPlain.TryDeliver(nc)
	case s.core == nil && e.kind == mixGeneric:
		s.serve(conn.NewConn(nc), transport)
		return true
	}
	return false
}

// stall holds c without an answer for a while and closes it, so that bytes
// of neither protocol tell a prober nothing.
func (s *Bridge) stall(ctx context.Context, c net.Conn) {
	t := time.NewTimer(10*time.Second + rand.N(20*time.Second))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	_ = c.Close()
}

// http3Detect is how long a connection with ALPN h3 gets to open its HTTP/3
// control stream. npc opens no unidirectional stream before its first
// bidirectional one, and ALPN cannot tell the two apart (§12).
const http3Detect = 100 * time.Millisecond

// listenQUIC opens a QUIC listener: the bridge's own (mix nil), where only
// carriers arrive, or the UDP side of the mixed port.
func (s *Bridge) listenQUIC(addr, route string, mix *mixEntry) error {
	n := connection.Network()
	cfg := &quic.Config{
		KeepAlivePeriod:    time.Duration(n.QUICKeepAlivePeriod) * time.Second,
		MaxIdleTimeout:     time.Duration(n.QUICMaxIdleTimeout) * time.Second,
		MaxIncomingStreams: n.QUICMaxIncomingStreams,
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{s.cert}, NextProtos: n.QUICALPNList}
	if s.core != nil {
		cfg = s.core.QUICConfig(cfg)
	}
	if s.core != nil || mix != nil {
		// nodes accept h3 and the quic_alpn list (§12)
		tlsCfg.NextProtos = append([]string{"h3"}, slices.DeleteFunc(slices.Clone(n.QUICALPNList), func(a string) bool { return a == "h3" })...)
	}
	if mix != nil {
		tlsCfg.GetCertificate = s.fallbackCert
	}
	l, err := conn.ListenQUIC(addr, tlsCfg, cfg)
	if err != nil {
		return err
	}
	slogx.Emit(context.Background(), log, &logevent.Listening, "route", "bridge", "listen", l.Addr().String(), "carrier", route)
	s.addCloser(l)
	_ = s.scope.Go("quic bridge listener", func(ctx context.Context) error {
		for {
			qc, err := l.Accept(ctx)
			if err != nil {
				return nil
			}
			s.serveQUIC(qc, mix)
		}
	})
	return nil
}

func (s *Bridge) serveQUIC(qc *quic.Conn, mix *mixEntry) {
	if s.store.GlobalDenied(qc.RemoteAddr(), "bridge") {
		_ = qc.CloseWithError(0, "")
		return
	}
	if s.conns.Go("quic carrier", func(ctx context.Context) error {
		defer func() { _ = qc.CloseWithError(0, "") }()
		// The connection ends with the bridge.
		stop := context.AfterFunc(ctx, func() { _ = qc.CloseWithError(0, "") })
		defer stop()
		if mix != nil && s.serveHTTP3(ctx, qc) {
			return nil
		}
		if s.core != nil {
			s.core.ServeQUIC(ctx, qc, mix == nil || mix.legacy)
			return nil
		}
		if mix != nil && !mix.legacy {
			return nil
		}
		// With the new protocol off the first stream is the old one's; the
		// handshake rejects anything else.
		done := make(chan struct{})
		conn.AcceptQuicStream(qc, func(c net.Conn) {
			s.serve(conn.NewConn(c), conn.TransportQUIC)
			close(done)
		})
		select {
		case <-done:
			<-qc.Context().Done()
		default:
		}
		return nil
	}) != nil {
		_ = qc.CloseWithError(0, "")
	}
}

// serveHTTP3 serves qc as HTTP/3 if it is: ALPN h3 and a unidirectional stream
// at once. It reports whether it took qc.
func (s *Bridge) serveHTTP3(ctx context.Context, qc *quic.Conn) bool {
	f := s.fallback.Load()
	if f == nil || qc.ConnectionState().TLS.NegotiatedProtocol != "h3" {
		return false
	}
	dctx, cancel := context.WithTimeout(ctx, http3Detect)
	defer cancel()
	first, err := qc.AcceptUniStream(dctx)
	if err != nil {
		return false
	}
	(*f).ServeHTTP3(qc, first) // without an engine qc just ends
	return true
}
