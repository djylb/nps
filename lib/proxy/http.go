package proxy

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/djylb/netx/httphead"
	"github.com/djylb/netx/httpproxy"
	"github.com/djylb/nps/lib/auth"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
)

// Timeouts of the HTTP proxy: the wait for the next request on a kept-alive
// client connection, which also bounds kept target connections, and the
// wait for a target's response head.
const (
	httpProxyIdleTimeout           = 30 * time.Second
	httpProxyResponseHeaderTimeout = 100 * time.Second
)

var httpConnectReply = []byte("HTTP/1.1 200 Connection established\r\n\r\n")

// newHTTPProxyServer returns the HTTP proxy server of s.
func newHTTPProxyServer(s *TunnelModeServer) *httpproxy.Server {
	return &httpproxy.Server{
		Auth: []httpproxy.Authenticator{
			httpproxy.BasicAuth{Realm: "Proxy", Check: func(_ context.Context, c net.Conn, user, password string) (bool, error) {
				if !s.checkProxyAccount(user, password) {
					proxyAuthFailed(c.RemoteAddr(), s.Task, user)
					return false, nil
				}
				return true, nil
			}},
			httpFallbackAuth{s},
		},
		Allow:  s.allowHTTPProxy,
		Handle: s.handleHTTPConnect,
		Dial:   s.dialHTTPProxy,
		// Target connections carry the client's address in their link, so
		// they serve that client only.
		ReuseKey: func(req *httpproxy.Request) (string, bool) {
			return req.Conn.RemoteAddr().String() + "\x00" + req.Dst.String(), true
		},
		// Other forwarding fields of the client pass through.
		ForwardHeader: func(_ *httpproxy.Request, h *httphead.Header) {
			h.Del("X-Forwarded-For")
		},
		IdleTimeout:           httpProxyIdleTimeout,
		IdleConnTimeout:       httpProxyIdleTimeout,
		ResponseHeaderTimeout: httpProxyResponseHeaderTimeout,
		OnError: func(c net.Conn, err error) {
			log.Debug("http proxy request failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
		},
	}
}

// checkProxyAccount checks credentials against the task's current accounts,
// accepting any when it has none.
func (s *TunnelModeServer) checkProxyAccount(user, password string) bool {
	u, p := socks5ClientCredentials(s.Task)
	accounts, authMap := model.GetAccountMap(s.Task.View().MultiAccount), model.GetAccountMap(s.Task.UserAuth)
	if !auth.NeedsAuth(u, p, accounts, authMap) {
		return true
	}
	return auth.CheckAuthWithAccountMap(user, password, u, p, accounts, authMap)
}

// httpFallbackAuth decides requests without Basic proxy credentials: it
// accepts them when the task has no accounts, and otherwise takes Basic
// credentials from Authorization when Proxy-Authorization is absent.
type httpFallbackAuth struct{ s *TunnelModeServer }

func (httpFallbackAuth) Scheme() string    { return "" }
func (httpFallbackAuth) Challenge() string { return "" }

func (a httpFallbackAuth) Authenticate(_ context.Context, req *httpproxy.Request, _ string) (string, error) {
	if !socks5NeedsAuth(a.s.Task) {
		return "", nil
	}
	if req.Header.Has("Proxy-Authorization") {
		return "", httpproxy.ErrAuthFailed // a scheme other than Basic
	}
	v := req.Header.Get("Authorization")
	if v == "" {
		return "", httpproxy.ErrAuthRequired
	}
	user, password, ok := auth.ParseBasicAuth(v)
	if !ok || !a.s.checkProxyAccount(user, password) {
		proxyAuthFailed(req.Conn.RemoteAddr(), a.s.Task, user)
		return "", httpproxy.ErrAuthFailed
	}
	return user, nil
}

// allowHTTPProxy applies the destination ACL and refuses requests once the
// task or client is over its flow or time limit.
func (s *TunnelModeServer) allowHTTPProxy(_ context.Context, req *httpproxy.Request) error {
	if v := s.Task.View(); v.Mode == "mixProxy" && v.DestAclMode != model.AclOff && !s.Task.AllowsDestination(req.Dst.String()) {
		destDenied(req.Conn.RemoteAddr(), s.Task, req.Dst.String())
		return &httpproxy.StatusError{Code: 403, Err: errProxyAccessDenied}
	}
	if err := conn.CheckFlowLimits(s.Task.Flow, "Task"); err != nil {
		return &httpproxy.StatusError{Code: 429, Err: err}
	}
	if err := conn.CheckFlowLimits(s.Task.Client.Flow, "Client"); err != nil {
		return &httpproxy.StatusError{Code: 429, Err: err}
	}
	return nil
}

// openHTTPLink opens a link to the destination of req through the client
// tunnel.
func (s *TunnelModeServer) openHTTPLink(c *conn.Conn, req *httpproxy.Request) (net.Conn, *conn.Link, bool, error) {
	target, link, isLocal, err := s.openClientLinkAcked(c, s.Task.Client, req.Dst.String(), conn.LinkTCP, s.Task.View().Target.LocalProxy, s.Task.Route().Node, s.Task)
	if errors.Is(err, errProxyAccessDenied) || err != nil && logevent.NetReason(err) == "permission" {
		err = &httpproxy.StatusError{Code: 403, Err: err}
	} else if err == nil && (target == nil || link == nil) {
		err = errors.New("no link to " + req.Dst.String())
	}
	return target, link, isLocal, err
}

// handleHTTPConnect serves CONNECT through the client tunnel and leaves
// requests to forward to the server.
func (s *TunnelModeServer) handleHTTPConnect(_ context.Context, req *httpproxy.Request) (bool, error) {
	if req.Method != "CONNECT" {
		return false, nil
	}
	//noinspection GoResourceLeak
	target, link, isLocal, err := s.openHTTPLink(conn.NewConn(req.Conn), req)
	if err != nil {
		return false, err
	}
	if _, err := req.Conn.Write(httpConnectReply); err != nil {
		_ = target.Close()
		return true, err
	}
	//noinspection GoResourceLeak
	clientConn := conn.NewConn(req.Conn)
	s.pipeClientConn(target, clientConn, link, s.Task.Client, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, nil, s.Task, isLocal, conn.LinkTCP)
	return true, nil
}

// dialHTTPProxy connects to the target of a forwarded request through the
// client tunnel, with the client's encryption, compression, rates and flows.
func (s *TunnelModeServer) dialHTTPProxy(_ context.Context, req *httpproxy.Request) (net.Conn, error) {
	//noinspection GoResourceLeak
	target, link, isLocal, err := s.openHTTPLink(conn.NewConn(req.Conn), req)
	if err != nil {
		return nil, err
	}
	raw := conn.GetConn(target, link.Crypt, link.Compress, s.Task.Client.Rate, s.Bridge.LinkCert(), isLocal, s.Task.RateLimiter())
	return conn.NewFlowConn(raw, s.Task.Flow, s.Task.Client.Flow), nil
}

// ProcessHttp serves the HTTP proxy requests of c.
func ProcessHttp(c net.Conn, s *TunnelModeServer) error {
	return s.httpProxy.ServeConn(context.Background(), c)
}
