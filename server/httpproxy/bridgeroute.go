package httpproxy

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/djylb/nps/lib/model"
)

// bridgeRoutes is what a server-side bridge offers the routes whose target is
// bridge://: the TLS this proxy ended stays visible to the new protocol, which
// a link to the bridge's in-memory listeners would hide. The bridge of npc has
// none of it.
type bridgeRoutes interface {
	CarrierRoute(target string, r *http.Request, next http.Handler) http.Handler
	ServeRouteTLS(tc *tls.Conn, head []byte, target string) bool
}

// bridgeTarget is the bridge target of a route that reaches the bridge of
// this process, "" for any other route.
func (s *HttpServer) bridgeTarget(h *model.Host, target *model.Target) string {
	if target == nil || !(s.AllowLocalProxy && target.LocalProxy || h.Client.Id < 0) {
		return ""
	}
	t, ok := strings.CutPrefix(strings.TrimSpace(target.TargetStr), "bridge://")
	if !ok || strings.ContainsAny(t, " \r\n") {
		return ""
	}
	return t
}

// bridgeCarrier returns the handler of a request that goes to the new
// protocol, nil to forward it as any other. What is no carrier gets the 404
// of an unknown site.
func (s *HttpServer) bridgeCarrier(req *proxyRequest, r *http.Request) http.Handler {
	b, ok := s.Bridge.(bridgeRoutes)
	if !ok {
		return nil
	}
	t := s.bridgeTarget(req.plan.host, req.route.Target)
	if t == "" {
		return nil
	}
	return b.CarrierRoute(t, r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeNotFound(w, s.fallback404())
	}))
}

// routeToBridge offers a connection of a TLS offload route to the new
// protocol. Without took it stays the route's, head being the bytes read.
func (s *HttpsServer) routeToBridge(h *model.Host, tc *tls.Conn) (head []byte, took bool) {
	b, ok := s.Bridge.(bridgeRoutes)
	if !ok {
		return nil, false
	}
	t := s.bridgeTarget(h, h.Route().Target)
	if t == "" {
		return nil, false
	}
	buf := make([]byte, 1)
	_ = tc.SetReadDeadline(time.Now().Add(tlsHandshakeTimeout))
	n, _ := tc.Read(buf)
	_ = tc.SetReadDeadline(time.Time{})
	head = buf[:n]
	return head, b.ServeRouteTLS(tc, head, t)
}
