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

// KeepClientForwarding undoes the clean-up ReverseProxy applies before
// Rewrite, so the client's request reaches the rewrite logic as sent: the
// forwarding headers come back unless the client listed them in Connection,
// and the query string keeps parameters ReverseProxy cannot parse.
func KeepClientForwarding(pr *httputil.ProxyRequest) {
	connection := pr.In.Header["Connection"]
	for _, name := range forwardingHeaders {
		v, ok := pr.In.Header[name]
		if !ok || httpguts.HeaderValuesContainsToken(connection, name) {
			continue
		}
		pr.Out.Header[name] = slices.Clone(v)
	}
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery
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
