package connection

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/portmux"
	"github.com/djylb/portmux/hostmatch"
)

// muxAcceptTimeout bounds both protocol recognition of a new connection and
// the wait for the target listener to accept it.
const muxAcceptTimeout = 10 * time.Second

// Kind names one of the listeners nps opens on its configured ports.
type Kind int

const (
	KindBridgeTCP Kind = iota
	KindBridgeTLS
	KindBridgeWS
	KindBridgeWSS
	KindHTTP
	KindHTTPS
	KindWeb
	kindCount
)

// httpMethodPrefixes are the first three bytes of the HTTP methods that are
// routed to the HTTP proxy, the web manager or the websocket bridge.
var httpMethodPrefixes = []string{"GET", "POS", "HEA", "PUT", "DEL", "CON", "OPT", "TRA"}

// portMux shares one TCP port between the bridge, the web manager and the
// HTTP/HTTPS proxies. Routing depends on which listeners are registered:
//   - "TST" goes to the TCP bridge;
//   - HTTP requests go to the only HTTP-like listener, or, when several are
//     registered, by Host to the web manager, then the websocket bridge,
//     and otherwise to the HTTP proxy;
//   - everything else goes to the HTTPS proxy, except TLS ClientHellos without
//     SNI or with the bridge host, which go to the TLS bridge when both are
//     registered; with neither, to the secure websocket bridge.
//
// A closed listener keeps its slot, so its connections are dropped instead of
// shifting to another protocol until it is replaced.
type portMux struct {
	mux         *portmux.Mux
	managerHost string
	clientHost  string

	mu     sync.Mutex
	routes [kindCount]*portmux.Route
}

func newPortMux(port int, managerHost, clientHost string) (*portMux, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	p := &portMux{
		mux: portmux.New(
			portmux.WithSniffTimeout(muxAcceptTimeout),
			portmux.WithDispatchTimeout(muxAcceptTimeout),
		),
		managerHost: hostPattern(managerHost),
		clientHost:  hostPattern(clientHost),
	}
	go func() { _ = p.mux.Serve(ln) }()
	return p, nil
}

// hostPattern returns host as an exact host pattern, or "" when it cannot
// be one.
func hostPattern(host string) string {
	host = netaddr.GetIpByAddr(host)
	if host == "" || host == "*" || host[0] == '*' || host[0] == '.' || hostmatch.CheckPattern(host) != nil {
		return ""
	}
	return host
}

func (p *portMux) Close() error {
	return p.mux.Close()
}

// register installs a new listener for kind, replacing and closing any
// listener it had, and updates the routes of all listeners for the new set.
// The new route, the removal of the old one and the changed matchers are
// applied in one batch, so connections never see a partial configuration
// and a failure changes nothing.
func (p *portMux) register(kind Kind) (net.Listener, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	routes := p.routes
	routes[kind] = nil
	rules := p.rules(func(k Kind) bool { return k == kind || routes[k] != nil })
	for {
		// Closed listeners keep their slot but are no longer routes of
		// the mux, so they are left out of the batch.
		var live [kindCount]bool
		for k, r := range routes {
			live[k] = r != nil && !r.Info().Removed
		}
		var added *portmux.Route
		err := p.mux.Batch(func(b *portmux.Batch) error {
			for k, r := range routes {
				if !live[k] {
					continue
				}
				if Kind(k) == kind {
					if err := b.Remove(r); err != nil {
						return err
					}
					continue
				}
				if err := b.SetPriority(r, rules[k].priority); err != nil {
					return err
				}
				if err := b.SetMatchers(r, rules[k].matchers...); err != nil {
					return err
				}
			}
			var err error
			added, err = b.Add(portmux.RouteConfig{Priority: rules[kind].priority, Matchers: rules[kind].matchers})
			return err
		})
		if err == nil {
			p.routes[kind] = added
			return added, nil
		}
		// A listener closed while the batch was prepared makes it fail
		// with net.ErrClosed; retry without it. Any other failure, or
		// net.ErrClosed from a closed mux, is returned.
		retry := false
		for k, r := range routes {
			if live[k] && r.Info().Removed {
				retry = true
			}
		}
		if !retry || !errors.Is(err, net.ErrClosed) {
			return nil, err
		}
	}
}

type routeRule struct {
	priority int
	matchers []portmux.Matcher
}

// rules computes the route of every listener for which has reports true.
func (p *portMux) rules(has func(Kind) bool) [kindCount]routeRule {
	var out [kindCount]routeRule
	methods := portmux.Prefix(httpMethodPrefixes...)
	// The catch-all route is an explicit Not(Prefix(...)) rather than a
	// Fallback route: a fallback also takes the connections whose own route
	// was closed and those other matchers ruled out (e.g. an HTTP request
	// for an unknown host when the HTTP proxy slot is closed), which would
	// send them to the HTTPS proxy instead of dropping them.
	other := portmux.Not(portmux.Prefix(append([]string{legacy.Magic}, httpMethodPrefixes...)...))

	out[KindBridgeTCP] = routeRule{matchers: []portmux.Matcher{portmux.Prefix(legacy.Magic)}}

	n := 0
	for _, k := range []Kind{KindHTTP, KindWeb, KindBridgeWS} {
		if has(k) {
			n++
		}
	}
	switch {
	case n > 1:
		if p.managerHost != "" {
			out[KindWeb] = routeRule{priority: 2, matchers: []portmux.Matcher{portmux.And(methods, portmux.HTTP1(portmux.Host(p.managerHost)))}}
		}
		if p.clientHost != "" {
			out[KindBridgeWS] = routeRule{priority: 1, matchers: []portmux.Matcher{portmux.And(methods, portmux.HTTP1(portmux.Host(p.clientHost)))}}
		}
		out[KindHTTP] = routeRule{matchers: []portmux.Matcher{methods}}
	case has(KindHTTP):
		out[KindHTTP] = routeRule{matchers: []portmux.Matcher{methods}}
	case has(KindWeb):
		out[KindWeb] = routeRule{matchers: []portmux.Matcher{methods}}
	default:
		out[KindBridgeWS] = routeRule{matchers: []portmux.Matcher{methods}}
	}

	switch {
	case has(KindHTTPS) && has(KindBridgeTLS):
		out[KindBridgeTLS] = routeRule{priority: 1, matchers: []portmux.Matcher{portmux.TLS(portmux.SNI("", p.clientHost))}}
		out[KindHTTPS] = routeRule{matchers: []portmux.Matcher{other}}
	case has(KindHTTPS):
		out[KindHTTPS] = routeRule{matchers: []portmux.Matcher{other}}
	case has(KindBridgeTLS):
		out[KindBridgeTLS] = routeRule{matchers: []portmux.Matcher{other}}
	default:
		out[KindBridgeWSS] = routeRule{matchers: []portmux.Matcher{other}}
	}
	return out
}
