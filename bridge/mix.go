package bridge

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
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
	// http3: the UDP side serves HTTP/3 too, so ALPN h3 is HTTP/3's and the
	// QUIC carrier answers only to the quic_alpn list (§13).
	http3 bool
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
		e.plain, e.legacy, e.http3 = n.MixPlain, n.MixLegacy, n.MixHTTP3()
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
// a single WebSocket bridge port, where the subprotocol decides (§12).
func (s *Bridge) mixRoute(kind mixKind) *conn.WSRoute {
	if kind == mixGeneric {
		return s.coreWS()
	}
	n := connection.Network()
	notFound := s.deniedHTTP(http.HandlerFunc(s.serveHTTPFallback))
	fw := s.coreForwarding()
	rest := s.httpCarriers(http.HandlerFunc(s.serveHTTPFallback), fw)
	route := &conn.WSRoute{NotFound: notFound, Paths: map[string]http.Handler{}}
	path := n.BridgePath
	if path == "" {
		path = "/"
	}
	for _, p := range append(slices.Clone(n.NewProtocolWSPaths), path) {
		route.Paths[p] = notFound
		h, err := s.core.WebSocketAnyProtocol(rest, fw)
		if err != nil {
			log.Error("new protocol websocket route", "path", p, "err", err)
			continue
		}
		route.Paths[p] = s.deniedHTTP(h)
	}
	return route
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
	//noinspection GoResourceLeak
	head, pc := readHead(c, 1)
	if len(head) == 0 {
		_ = c.Close()
		return
	}
	if head[0] == 0x16 {
		cfg := s.tlsConfig()
		cfg.GetCertificate = s.fallbackCert
		//noinspection GoResourceLeak
		s.admitMix(ctx, tls.Server(pc, cfg), conn.TransportTLS, e)
		return
	}
	if e.plain {
		s.admitMix(ctx, pc, conn.TransportTCP, e)
		return
	}
	//noinspection GoResourceLeak
	magic, nc := readHead(pc, len(legacy.Magic))
	if connection.IsHTTP(magic) && s.coreFallback(nc, nil) {
		return
	}
	s.core.Stall(ctx, nc)
}

// admitMix gives a carrier to the new protocol's admission.
func (s *Bridge) admitMix(ctx context.Context, c net.Conn, carrier string, e *mixEntry) {
	other := func(c net.Conn, st *tls.ConnectionState) bool { return s.mixOther(c, st, e) }
	s.core.ServeMixed(ctx, c, carrier, other)
}

// mixOther routes what the new protocol's admission did not take: the old
// protocol's magic and HTTP. It owns c when it returns true.
func (s *Bridge) mixOther(c net.Conn, st *tls.ConnectionState, e *mixEntry) bool {
	//noinspection GoResourceLeak
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
		//noinspection GoResourceLeak
		s.serve(conn.NewConn(nc), transport)
		return true
	case connection.IsHTTP(head):
		if st != nil {
			return e.httpTLS.TryDeliver(&conn.TLSStateConn{Conn: nc, State: st})
		}
		return e.httpPlain.TryDeliver(nc)
	}
	return false
}

// listenQUIC opens a QUIC listener: the bridge's own (mix nil), where only
// carriers arrive, or the UDP side of the mixed port.
func (s *Bridge) listenQUIC(addr, route string, mix *mixEntry) error {
	return s.serveQUICOn(addr, route, mix, nil)
}

// serveQUICOn is listenQUIC on pc, or on addr when pc is nil.
func (s *Bridge) serveQUICOn(addr, route string, mix *mixEntry, pc net.PacketConn) error {
	n := connection.Network()
	cfg := &quic.Config{
		KeepAlivePeriod:    time.Duration(n.QUICKeepAlivePeriod) * time.Second,
		MaxIdleTimeout:     time.Duration(n.QUICMaxIdleTimeout) * time.Second,
		MaxIncomingStreams: n.QUICMaxIncomingStreams,
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{s.cert}}
	cfg = s.core.QUICConfig(cfg)
	http3 := mix != nil && mix.http3
	if err := n.CheckQUICALPN(http3); err != nil {
		return fmt.Errorf("quic listener %s: %w", addr, err)
	}
	// ALPN alone tells the protocols apart (§12, §13): quic_alpn names are
	// the old protocol's, new_protocol_quic_alpn the new one's, h3 HTTP/3's
	// where it shares the port.
	tlsCfg.NextProtos = slices.Clone(n.NewProtocolQUICALPN)
	if mix == nil || mix.legacy {
		tlsCfg.NextProtos = append(tlsCfg.NextProtos, n.QUICALPNList...)
	}
	if http3 {
		tlsCfg.NextProtos = append(tlsCfg.NextProtos, "h3")
	}
	if mix != nil {
		tlsCfg.GetCertificate = s.fallbackCert
	}
	var l *quic.Listener
	var err error
	if pc != nil {
		l, err = quic.Listen(pc, tlsCfg, cfg)
	} else {
		l, err = conn.ListenQUIC(addr, tlsCfg, cfg)
	}
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
		alpn := qc.ConnectionState().TLS.NegotiatedProtocol
		n := connection.Network()
		switch {
		case mix != nil && mix.http3 && alpn == "h3":
			s.serveHTTP3(qc)
		case !slices.Contains(n.QUICALPNList, alpn):
			s.core.ServeQUIC(ctx, qc)
		case mix != nil && !mix.legacy:
			// the old protocol is off on this port
		default:
			// The first stream is the old protocol's; the handshake
			// rejects anything else.
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
		}
		return nil
	}) != nil {
		_ = qc.CloseWithError(0, "")
	}
}

// serveHTTP3 serves qc as HTTP/3 until it ends; without an engine qc just
// ends.
func (s *Bridge) serveHTTP3(qc *quic.Conn) {
	if f := s.fallback.Load(); f != nil {
		(*f).ServeHTTP3(qc, s.http3Carriers)
	}
}

// http3Carriers gives the requests of the bridge path to the new protocol;
// anything else, and what is no carrier there, stays with next.
func (s *Bridge) http3Carriers(next http.Handler) http.Handler {
	h, _ := s.routeCarriers.Load().(http.Handler)
	if h == nil {
		return next
	}
	n := connection.Network()
	carriers := withRouteNext(h, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == n.BridgePath || slices.Contains(n.NewProtocolWSPaths, r.URL.Path) {
			carriers.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
