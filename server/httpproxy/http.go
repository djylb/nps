package httpproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/httprewrite"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/slogx"
)

type HttpServer struct {
	*HttpProxy
	httpStatus   atomic.Bool
	httpListener net.Listener
	httpServer   *http.Server
}

func NewHttpServer(httpProxy *HttpProxy, l net.Listener) *HttpServer {
	hs := &HttpServer{
		HttpProxy:    httpProxy,
		httpListener: l,
	}
	hs.httpServer = hs.NewServer(hs.HttpPort, "http")
	return hs
}

func (s *HttpServer) Start() error {
	if !s.httpStatus.CompareAndSwap(false, true) {
		return errors.New("http server already started")
	}
	if err := s.httpServer.Serve(s.httpListener); err != nil {
		s.httpStatus.Store(false)
		return err
	}
	s.httpStatus.Store(false)
	return nil
}

func (s *HttpServer) Close() error {
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	s.httpStatus.Store(false)
	return nil
}

func (s *HttpServer) writeErrorPage(rw http.ResponseWriter, statusCode int, content []byte) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(statusCode)
	_, _ = rw.Write(content)
}

func (s *HttpServer) writeLimitErrorPage(rw http.ResponseWriter, errMsg string) bool {
	var content []byte
	switch {
	case strings.Contains(errMsg, "time limit exceeded"):
		content = s.TimeLimitErrorContent
	case strings.Contains(errMsg, "flow limit exceeded"):
		content = s.FlowLimitErrorContent
	default:
		return false
	}

	if content == nil {
		return false
	}

	s.writeErrorPage(rw, http.StatusTooManyRequests, content)
	return true
}

// abortRequest aborts the request being served without a response: it
// closes the connection of an HTTP/1 request and resets the stream of an
// HTTP/2 or HTTP/3 one, which, unlike HTTP/1, cannot be hijacked.
func abortRequest() {
	panic(http.ErrAbortHandler)
}

// handleProxy runs a request through the stages described in plan.go.
func (s *HttpServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// entry
	host, err := s.store.GetInfoByHost(r.Host, r)
	if err != nil || host.IsClose {
		proxyLog.Debug("host not found", "route", r.Host, "path", r.RequestURI, "src_ip", srcIP(r.RemoteAddr))
		if !s.ErrorAlways {
			abortRequest()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(s.ErrorContent)
		return
	}
	if proxy.SourceDeniedString(r.RemoteAddr, s.Bridge.GlobalSource(), host.Client, &host.SourceLists, host.Host) {
		abortRequest()
	}
	p := s.plans.get(host, s.compilePlan)
	req := &proxyRequest{plan: p, remote: r.RemoteAddr, tls: r.TLS != nil}

	// scheme
	if p.acme && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		s.Acme.HandleHTTPChallenge(w, r)
		return
	}
	if s.HttpOnlyPass != "" && r.Header.Get("X-NPS-Http-Only") == s.HttpOnlyPass {
		req.httpOnly = true
		r.Header.Del("X-NPS-Http-Only")
	}
	if p.autoHttps && !req.httpOnly && !req.tls {
		http.Redirect(w, r, s.httpsURL(r), http.StatusMovedPermanently)
		return
	}

	// admission
	release, err := s.AcquireForwarding(host.Client, host.ControlGroup())
	if err != nil {
		http.Error(w, "Access denied: "+err.Error(), http.StatusTooManyRequests)
		emitLimit(host, err)
		return
	}
	defer release()

	// authentication. Upgrade and CONNECT requests may carry protocols whose
	// clients cannot send Basic credentials, so they proceed unauthenticated;
	// such a request only reaches the client if the backend completes the
	// handshake.
	req.authed = s.Auth(r, nil, host.Client.Cnf.U, host.Client.Cnf.P, host.MultiAccount, host.UserAuth) == nil
	if !req.authed && !isTunnelRequest(r) {
		emitAuthDenied(host, r.RemoteAddr)
		writeUnauthorized(w)
		return
	}

	// rewrite
	p.rewritePath(r)
	if p.redirect != nil {
		if !req.authed {
			emitAuthDenied(host, r.RemoteAddr)
			writeUnauthorized(w)
			return
		}
		http.Redirect(w, r, p.redirect.expand(r), http.StatusTemporaryRedirect)
		return
	}
	// upstream
	req.route = host.Route()
	req.sni = r.Host
	if p.hostChange != "" {
		req.sni = p.hostChange
	}
	r = r.WithContext(context.WithValue(r.Context(), requestKey{}, req))

	// forward
	if isTunnelRequest(r) {
		s.forwardTunnel(w, r, req)
		return
	}
	if !proxyLog.Enabled(r.Context(), logevent.HTTPAccess.Level) {
		p.proxy.ServeHTTP(w, r)
		return
	}
	start := time.Now()
	aw := &accessWriter{ResponseWriter: w}
	p.proxy.ServeHTTP(aw, r)
	if aw.status == 0 {
		aw.status = http.StatusOK
	}
	slogx.Emit(r.Context(), proxyLog, &logevent.HTTPAccess, "route", host.Host, "src_ip", srcIP(r.RemoteAddr),
		"method", r.Method, "host", r.Host, "path", r.URL.Path, "status", aw.status, "bytes", aw.bytes,
		"dur_ms", time.Since(start).Milliseconds())
}

// accessWriter records the status and size of a response for http_access.
// Unwrap lets http.ResponseController reach the flusher and hijacker of the
// writer it wraps.
type accessWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *accessWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *accessWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps streamed responses streaming through the wrapper.
func (w *accessWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// httpsURL is the HTTPS URL of r, on the HTTPS port of the proxy.
func (s *HttpServer) httpsURL(r *http.Request) string {
	host := netaddr.Host(r.Host)
	if s.HttpsPort != 443 {
		host = net.JoinHostPort(host, s.HttpsPortStr)
	} else if strings.IndexByte(host, ':') >= 0 {
		host = "[" + host + "]"
	}
	return "https://" + host + r.RequestURI
}

// newReverseProxy returns the reverse proxy of the regular requests of p.
func (s *HttpServer) newReverseProxy(p *hostPlan) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := requestOf(pr.In.Context())
			proxy.KeepClientForwarding(pr)
			out := pr.Out
			out.URL.Scheme = p.backendScheme
			// Header rules cannot change connection-scoped headers; ReverseProxy
			// has already set them for the outbound hop.
			hop := snapshotHeaders(out.Header, hopHeaders)
			p.rewriteHeaders(out, pr.In, !req.httpOnly)
			restoreHeaders(out.Header, hopHeaders, hop)
			out.URL.Host = pr.In.Host
			if req.httpOnly {
				out.Header.Del("X-Forwarded-For")
			} else {
				proxy.AppendForwardedFor(out.Header, pr.In.RemoteAddr)
			}
		},
		Transport:  p.transports,
		BufferPool: conn.BufPoolCopy,
		ModifyResponse: func(resp *http.Response) error {
			if p.autoCORS {
				origin := resp.Request.Header.Get("Origin")
				if origin != "" && resp.Header.Get("Access-Control-Allow-Origin") == "" {
					proxyLog.Debug("cors headers set", "origin", origin)
					resp.Header.Set("Access-Control-Allow-Origin", origin)
					resp.Header.Set("Access-Control-Allow-Credentials", "true")
				}
			}
			if p.altSvc != "" && requestOf(resp.Request.Context()).tls {
				resp.Header.Set("Alt-Svc", p.altSvc)
			}
			applyResponseRules(resp, p.respRules)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			s.plans.drop(p)
			if err == io.EOF {
				proxyLog.Debug("backend closed before response", "route", p.host.Host, "status", 521)
				rw.WriteHeader(521)
				return
			}
			proxyLog.Debug("proxy error", "route", p.host.Host, "method", req.Method, "path", req.URL.Path, "err", err)

			errMsg := err.Error()
			idx := strings.Index(errMsg, "Task")
			if idx == -1 {
				idx = strings.Index(errMsg, "Client")
			}
			if idx != -1 {
				if s.writeLimitErrorPage(rw, errMsg) {
					return
				}
				http.Error(rw, errMsg[idx:], http.StatusTooManyRequests)
			} else {
				s.writeErrorPage(rw, http.StatusBadGateway, s.ErrorContent)
			}
		},
	}
}

// hostTransports are the backend transports of one host. The route of each
// request chooses between them: a PROXY header names one visitor, so the
// connection of a route that sends one serves no other request. On the routes
// without it a reused connection stays with the instance that it was dialed to,
// whichever route the request picked.
type hostTransports struct {
	reuse, single *http.Transport
}

func (t *hostTransports) forRoute(route model.Route) *http.Transport {
	if route.Target.ProxyProtocol != 0 {
		return t.single
	}
	return t.reuse
}

func (t *hostTransports) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.forRoute(requestOf(r.Context()).route).RoundTrip(r)
}

func (t *hostTransports) CloseIdleConnections() {
	t.reuse.CloseIdleConnections()
	t.single.CloseIdleConnections()
}

// newTransports returns the transports of a host; noKeepAlive keeps no
// connection for a later request on any route.
func (s *HttpServer) newTransports(noKeepAlive bool) *hostTransports {
	return &hostTransports{reuse: s.newTransport(noKeepAlive), single: s.newTransport(true)}
}

func (s *HttpServer) newTransport(disableKeepAlives bool) *http.Transport {
	return &http.Transport{
		ResponseHeaderTimeout: s.ResponseHeaderTimeout,
		DisableKeepAlives:     disableKeepAlives,
		DialContext:           s.DialContext,
		DialTLSContext:        s.DialTlsContext,
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   256,
		IdleConnTimeout:       90 * time.Second,
	}
}

// openLink opens a link to the route of req through its client. dialing is
// called with each target address tried, failed with each that failed.
func (s *HttpServer) openLink(req *proxyRequest, dialing func(addr string), failed func(addr string, err error)) (io.ReadWriteCloser, error) {
	h, route := req.plan.host, req.route
	isLocal := s.AllowLocalProxy && route.Target.LocalProxy || h.Client.Id < 0
	var link *conn.Link
	var target net.Conn
	err := route.Target.Dial(func(addr string) (bool, error) {
		if dialing != nil {
			dialing(addr)
		}
		link = conn.NewLink(conn.LinkTCP, addr, h.Client.Cnf.Crypt, h.Client.Cnf.Compress, req.remote, isLocal)
		link.Option.Node = route.Node
		var err error
		target, err = s.Bridge.Session(h.Client.Id).OpenLink(link, 0)
		if err != nil {
			failed(addr, err)
		}
		return isLocal, err
	})
	if err != nil {
		return nil, err
	}
	return conn.GetConn(target, link.Crypt, link.Compress, h.Client.Rate, s.Bridge.LinkCert(), isLocal, h.RateLimiter()), nil
}

// sendProxyHeader writes the PROXY header of req's route, if it has one, to c.
func sendProxyHeader(ctx context.Context, c net.Conn, req *proxyRequest) error {
	if req.route.Target.ProxyProtocol == 0 {
		return nil
	}
	ra, _ := net.ResolveTCPAddr("tcp", req.remote)
	if ra == nil || ra.IP == nil {
		ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
	}
	la, _ := ctx.Value(http.LocalAddrContextKey).(*net.TCPAddr)
	hdr := conn.ProxyProtocolHeader(req.route.Target.ProxyProtocol, ra, la)
	if hdr == nil {
		return nil
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := hdr.WriteTo(c); err != nil {
		return err
	}
	_ = c.SetWriteDeadline(time.Time{})
	return nil
}

// forwardTunnel forwards an upgrade or CONNECT request: it sends the
// request to the backend and, once the backend completes the handshake,
// relays the connection both ways.
func (s *HttpServer) forwardTunnel(w http.ResponseWriter, r *http.Request, req *proxyRequest) {
	p, host := req.plan, req.plan.host
	rawConn, err := s.openLink(req, func(addr string) {
		slogx.Emit(context.Background(), proxyLog, &logevent.HTTPAccess, "route", host.Host, "src_ip", srcIP(r.RemoteAddr), "method", r.Method, "host", r.Host, "path", r.URL.Path, "target", addr)
	}, func(addr string, err error) {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", host.Host, "target", addr, "reason", logevent.NetReason(err), "err", err)
	})
	if err != nil {
		if errors.Is(err, model.ErrNoTarget) {
			slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", host.Host, "target", r.Host, "reason", logevent.NetReason(err), "err", err)
		}
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	// Every path below closes netConn, which wraps wsConn, or hands it to the relay.
	//noinspection GoResourceLeak
	wsConn := netx.WrapConn(rawConn, nil)
	var netConn net.Conn = wsConn

	if err := sendProxyHeader(r.Context(), netConn, req); err != nil {
		_ = netConn.Close()
		return
	}

	if p.backendTLS {
		tlsConn, err := tlsconn.Client(r.Context(), netConn, backendTLSConfig(req.sni))
		if err != nil {
			slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, slogx.KeyMsg, "backend tls handshake failed", "route", host.Host, "target", req.sni, "reason", logevent.NetReason(err), "err", err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		netConn = tlsConn
	}

	p.rewriteUpgrade(r, req)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		proxyLog.Error("tunnel request cannot hijack connection", "route", host.Host)
		return
	}

	r.Close = false
	if err := r.Write(netConn); err != nil {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, slogx.KeyMsg, "handshake write to backend failed", "route", host.Host, "target", req.sni, "reason", logevent.NetReason(err), "err", err)
		_ = netConn.Close()
		s.writeErrorPage(w, http.StatusBadGateway, s.ErrorContent)
		return
	}

	backendReader := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(backendReader, r)
	if err != nil {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, slogx.KeyMsg, "handshake read from backend failed", "route", host.Host, "target", req.sni, "reason", logevent.NetReason(err), "err", err)
		_ = netConn.Close()
		s.writeErrorPage(w, http.StatusBadGateway, s.ErrorContent)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Only a successful handshake turns the connection into a tunnel; any other
	// backend answer is relayed as a regular response and the connection ends.
	good := (r.Method == http.MethodConnect && resp.StatusCode == http.StatusOK) ||
		(r.Method != http.MethodConnect && resp.StatusCode == http.StatusSwitchingProtocols)
	if !good {
		proxyLog.Debug("backend refused tunnel handshake", "route", host.Host, "status", resp.StatusCode, "src_ip", srcIP(r.RemoteAddr))
		if !req.authed {
			// An unauthenticated client never sees a backend response.
			_ = netConn.Close()
			emitAuthDenied(host, r.RemoteAddr)
			writeUnauthorized(w)
			return
		}
		s.relayHandshakeRefusal(w, resp, p.respRules, netConn)
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		proxyLog.Debug("hijack failed", "route", host.Host, "err", err)
		return
	}

	if err := resp.Write(clientBuf); err != nil {
		proxyLog.Debug("handshake write to visitor failed", "route", host.Host, "err", err)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}
	if err := clientBuf.Flush(); err != nil {
		proxyLog.Debug("handshake flush to visitor failed", "route", host.Host, "err", err)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}

	if backendReader.Buffered() > 0 {
		pending := make([]byte, backendReader.Buffered())
		if _, err := backendReader.Read(pending); err == nil {
			netConn = netx.NewPrefixConn(netConn, pending)
		} else {
			proxyLog.Debug("backend buffered read failed", "route", host.Host, "err", err)
			_ = netConn.Close()
			_ = clientConn.Close()
			return
		}
	}

	bufReader := clientBuf.Reader
	if bufReader.Buffered() > 0 {
		pending := make([]byte, bufReader.Buffered())
		if _, err := bufReader.Read(pending); err != nil {
			proxyLog.Debug("visitor buffered read failed", "route", host.Host, "err", err)
			_ = netConn.Close()
			_ = clientConn.Close()
			return
		}
		clientConn = netx.NewPrefixConn(clientConn, pending)
	}

	conn.Join(clientConn, netConn, []conn.Flow{host.Flow, host.Client.Flow}, s.Task, r.RemoteAddr)
}

// isTunnelRequest reports whether r asks to turn the connection into a tunnel:
// CONNECT (including HTTP/2 extended CONNECT) or an HTTP/1.1 Upgrade.
func isTunnelRequest(r *http.Request) bool {
	return r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" || r.Header.Get(":protocol") != ""
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
	http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
}

// hopHeaders are connection-scoped headers that must not be relayed.
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// snapshotHeaders copies the values of names in h.
func snapshotHeaders(h http.Header, names []string) http.Header {
	saved := make(http.Header, len(names))
	for _, name := range names {
		key := http.CanonicalHeaderKey(name)
		if v, ok := h[key]; ok {
			saved[key] = slices.Clone(v)
		}
	}
	return saved
}

// restoreHeaders resets names in h to the values recorded in saved, removing
// those saved does not hold.
func restoreHeaders(h http.Header, names []string, saved http.Header) {
	for _, name := range names {
		key := http.CanonicalHeaderKey(name)
		if v, ok := saved[key]; ok {
			h[key] = v
		} else {
			delete(h, key)
		}
	}
}

// relayHandshakeRefusal writes the refusal while the backend connection is
// still open, since resp.Body streams from it, and closes it afterwards. The
// tunnel connection ignores read deadlines, so the body copy is bounded by
// closing the connection once the response timeout elapses.
func (s *HttpServer) relayHandshakeRefusal(w http.ResponseWriter, resp *http.Response, respRules []httprewrite.Rule, backend net.Conn) {
	defer func() { _ = backend.Close() }()
	if s.ResponseHeaderTimeout > 0 {
		timer := time.AfterFunc(s.ResponseHeaderTimeout, func() { _ = backend.Close() })
		defer timer.Stop()
	}
	s.writeHandshakeRefusal(w, resp, respRules)
}

// writeHandshakeRefusal relays a backend response that did not complete an
// upgrade or CONNECT handshake as an ordinary HTTP response and asks the
// server to close the client connection afterwards.
func (s *HttpServer) writeHandshakeRefusal(w http.ResponseWriter, resp *http.Response, respRules []httprewrite.Rule) {
	if resp.StatusCode < http.StatusOK {
		s.writeErrorPage(w, http.StatusBadGateway, s.ErrorContent)
		return
	}
	applyResponseRules(resp, respRules)
	header := w.Header()
	for k, vv := range resp.Header {
		header[k] = vv
	}
	for _, v := range resp.Header.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}
	for _, name := range hopHeaders {
		header.Del(name)
	}
	header.Set("Connection", "close")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

const (
	// serverReadHeaderTimeout bounds reading a request header, so that slow
	// clients cannot hold connections open without sending a request.
	serverReadHeaderTimeout = 10 * time.Second
	// serverIdleTimeout closes keep-alive connections idle between requests.
	serverIdleTimeout = 90 * time.Second
)

func (s *HttpServer) NewServer(port int, scheme string) *http.Server {
	return &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = scheme
			s.handleProxy(w, r)
		}),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		ErrorLog:          slogx.HTTPErrorLog(proxyLog, scheme),
	}
}

// DialContext opens the backend connection of a regular request, whose
// proxyRequest ctx carries.
func (s *HttpServer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	req := requestOf(ctx)
	h := req.plan.host
	rawConn, err := s.openLink(req, nil, func(addr string, err error) {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", h.Host, "target", addr, "reason", logevent.NetReason(err), "err", err)
	})
	if err != nil {
		if errors.Is(err, model.ErrNoTarget) {
			slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, "route", h.Host, "target", h.Id, "reason", logevent.NetReason(err), "err", err)
		}
		return nil, err
	}
	flowConn := conn.NewFlowConn(rawConn, h.Flow, h.Client.Flow)
	if err := sendProxyHeader(ctx, flowConn, req); err != nil {
		_ = flowConn.Close()
		return nil, fmt.Errorf("write PROXY header: %w", err)
	}
	return flowConn, nil
}

func (s *HttpServer) DialTlsContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := s.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	tlsConn, err := tlsconn.Client(ctx, c, backendTLSConfig(requestOf(ctx).sni))
	if err != nil {
		slogx.Emit(context.Background(), proxyLog, &logevent.RelayFailed, slogx.KeyMsg, "backend tls handshake failed", "route", requestOf(ctx).plan.host.Host, "target", requestOf(ctx).sni, "reason", logevent.NetReason(err), "err", err)
		return nil, err
	}
	return tlsConn, nil
}

// backendTLSConfig is the TLS client configuration for a backend reached
// with the SNI of the client request.
func backendTLSConfig(sni string) *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, ServerName: netaddr.Host(sni)}
}
