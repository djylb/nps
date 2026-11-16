package proxy

import (
	"net/http"
	"net/http/httputil"
	"slices"

	"github.com/djylb/netx/forwarded"
	"golang.org/x/net/http/httpguts"
)

// forwardingHeaders are the client-supplied headers ReverseProxy strips from
// the outbound request before it calls Rewrite.
var forwardingHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
}

// clientForwarding are the forwarding headers a client can forge: only those
// of a trusted proxy are passed on.
var clientForwarding = append(slices.Clone(forwardingHeaders), "X-Forwarded-Port", "X-Real-Ip")

// KeepClientForwarding undoes the clean-up ReverseProxy applies before
// Rewrite, so the client's request reaches the rewrite logic as sent: the
// query string keeps parameters ReverseProxy cannot parse and, when trusted
// says the client is a trusted proxy, the forwarding headers come back unless
// it listed them in Connection. An untrusted client's forwarding headers are
// all dropped, so the backend sees only those set from what was observed.
func KeepClientForwarding(pr *httputil.ProxyRequest, trusted bool) {
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery
	if !trusted {
		DropClientForwarding(pr.Out.Header)
		return
	}
	connection := pr.In.Header["Connection"]
	for _, name := range forwardingHeaders {
		v, ok := pr.In.Header[name]
		if !ok || httpguts.HeaderValuesContainsToken(connection, name) {
			continue
		}
		pr.Out.Header[name] = slices.Clone(v)
	}
}

// DropClientForwarding removes the forwarding headers a client sent from h.
func DropClientForwarding(h http.Header) {
	for _, name := range clientForwarding {
		delete(h, name)
	}
}

// AppendForwardedFor appends the client IP taken from remoteAddr to the
// X-Forwarded-For header of h, creating the header when it is absent.
func AppendForwardedFor(h http.Header, remoteAddr string) {
	peer, ok := forwarded.ParseNode(remoteAddr)
	if !ok {
		return
	}
	h.Set("X-Forwarded-For", string(forwarded.AppendXForwardedFor(nil, h["X-Forwarded-For"], peer.Addr())))
}
