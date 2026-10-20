package httpproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/proxy"
)

type ctxKey string

const (
	ctxRemoteAddr ctxKey = "nps_remote_addr"
	ctxHost       ctxKey = "nps_host"
	ctxSNI        ctxKey = "nps_sni"
)

type HttpServer struct {
	*HttpProxy
	httpStatus   bool
	httpListener net.Listener
	httpServer   *http.Server
}

func NewHttpServer(httpProxy *HttpProxy, l net.Listener) *HttpServer {
	hs := &HttpServer{
		httpStatus:   false,
		HttpProxy:    httpProxy,
		httpListener: l,
	}
	hs.httpServer = hs.NewServer(hs.HttpPort, "http")
	return hs
}

func (s *HttpServer) Start() error {
	if s.httpStatus {
		return errors.New("http server already started")
	}
	s.httpStatus = true
	if err := s.httpServer.Serve(s.httpListener); err != nil {
		s.httpStatus = false
		return err
	}
	s.httpStatus = false
	return nil
}

func (s *HttpServer) Close() error {
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	s.httpStatus = false
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

func (s *HttpServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Get host
	host, err := file.GetDb().GetInfoByHost(r.Host, r)
	if err != nil || host.IsClose {
		logs.Debug("Host not found: %s %s %s", r.URL.Scheme, r.Host, r.RequestURI)
		if !s.ErrorAlways {
			abortRequest()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(s.ErrorContent)
		return
	}

	// source access lists
	if proxy.SourceDeniedString(r.RemoteAddr, host.Client, &host.SourceLists, "host "+strconv.Itoa(host.Id)+" ("+host.Host+")") {
		abortRequest()
	}

	// AutoSSL
	if s.autoSSL(host) && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		s.Acme.HandleHTTPChallenge(w, r)
		return
	}

	// HTTP-Only
	isHttpOnlyRequest := s.HttpOnlyPass != "" && r.Header.Get("X-NPS-Http-Only") == s.HttpOnlyPass
	if isHttpOnlyRequest {
		r.Header.Del("X-NPS-Http-Only")
	}

	// Auto 301 to HTTPS
	if !isHttpOnlyRequest && host.AutoHttps && r.TLS == nil {
		redirectHost := common.RemovePortFromHost(r.Host)
		if s.HttpsPort != 443 {
			redirectHost += ":" + s.HttpsPortStr
		}
		http.Redirect(w, r, "https://"+redirectHost+r.RequestURI, http.StatusMovedPermanently)
		return
	}

	// Path Rewrite
	if host.PathRewrite != "" && strings.HasPrefix(r.URL.Path, host.Location) {
		if !host.CompatMode {
			r.Header.Set("X-Original-Path", r.URL.Path)
		}
		r.URL.Path = host.PathRewrite + r.URL.Path[len(host.Location):]
	}

	// Check flow and conn
	if err := s.CheckFlowAndConnNum(host.Client); err != nil {
		http.Error(w, "Access denied: "+err.Error(), http.StatusTooManyRequests)
		logs.Warn("Connection limit exceeded, client id %d, host id %d, error %v", host.Client.Id, host.Id, err)
		return
	}
	defer host.Client.CutConn()
	host.AddConn()
	defer host.CutConn()

	// HTTP Auth. Upgrade and CONNECT requests may carry protocols whose
	// clients cannot send Basic credentials, so they proceed unauthenticated;
	// such a request only reaches the client if the backend completes the
	// handshake.
	authed := true
	if err := s.Auth(r, nil, host.Client.Cnf.U, host.Client.Cnf.P, host.MultiAccount, host.UserAuth); err != nil {
		if !isTunnelRequest(r) {
			logs.Warn("Unauthorized request from %s", r.RemoteAddr)
			writeUnauthorized(w)
			return
		}
		authed = false
	}

	// 307 Temporary Redirect
	if host.RedirectURL != "" {
		if !authed {
			logs.Warn("Unauthorized request from %s", r.RemoteAddr)
			writeUnauthorized(w)
			return
		}
		redirectURL := s.ChangeRedirectURL(r, host.RedirectURL)
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	logs.Debug("%s request, method %s, host %s, url %s, remote address %s", r.URL.Scheme, r.Method, r.Host, r.URL.Path, r.RemoteAddr)

	sni := r.Host
	if host.HostChange != "" {
		sni = host.HostChange
	}
	ctx := context.WithValue(r.Context(), ctxRemoteAddr, r.RemoteAddr)
	ctx = context.WithValue(ctx, ctxHost, host)
	ctx = context.WithValue(ctx, ctxSNI, sni)
	r = r.WithContext(ctx)

	// WebSocket
	if isTunnelRequest(r) {
		logs.Trace("Handling websocket from %s", r.RemoteAddr)
		s.handleWebsocket(w, r, host, isHttpOnlyRequest, authed)
		return
	}

	var tr *http.Transport
	if v, ok := s.HttpProxyCache.Get(host.Id); ok {
		tr = v.(*http.Transport)
	} else {
		v, _ := s.HttpProxyCache.LoadOrStore(host.Id, &http.Transport{
			ResponseHeaderTimeout: s.ResponseHeaderTimeout,
			DisableKeepAlives:     host.CompatMode,
			DialContext:           s.DialContext,
			DialTLSContext:        s.DialTlsContext,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
		})
		tr = v.(*http.Transport)
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			proxy.KeepClientForwarding(pr)
			req := pr.Out
			if host.TargetIsHttps {
				req.URL.Scheme = "https"
			} else {
				req.URL.Scheme = "http"
			}
			// Header rules cannot change connection-scoped headers; ReverseProxy
			// has already set them for the outbound hop.
			hop := snapshotHeaders(req.Header, hopHeaders)
			s.changeHostAndHeader(req, pr.In.Header, host.HostChange, host.HeaderChange, isHttpOnlyRequest)
			restoreHeaders(req.Header, hopHeaders, hop)
			req.URL.Host = r.Host
			if isHttpOnlyRequest {
				req.Header.Del("X-Forwarded-For")
			} else {
				proxy.AppendForwardedFor(req.Header, pr.In.RemoteAddr)
			}
		},
		Transport: tr,
		//FlushInterval: 100 * time.Millisecond,
		BufferPool: common.BufPoolCopy,
		ModifyResponse: func(resp *http.Response) error {
			// CORS
			if host.AutoCORS {
				origin := resp.Request.Header.Get("Origin")
				if origin != "" && resp.Header.Get("Access-Control-Allow-Origin") == "" {
					logs.Debug("ModifyResponse: setting CORS headers for origin=%s", origin)
					resp.Header.Set("Access-Control-Allow-Origin", origin)
					resp.Header.Set("Access-Control-Allow-Credentials", "true")
				}
			}
			// H3
			if s.Http3Port > 0 && r.TLS != nil && !host.HttpsJustProxy && !host.CompatMode {
				resp.Header.Set("Alt-Svc", `h3=":`+s.Http3PortStr+`"; ma=86400`)
			}
			s.ChangeResponseHeader(resp, host.RespHeaderChange)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			s.HttpProxyCache.Remove(host.Id)
			if err == io.EOF {
				logs.Info("ErrorHandler: io.EOF encountered, writing 521")
				rw.WriteHeader(521)
				return
			}
			logs.Debug("ErrorHandler: proxy error: method=%s, URL=%s, error=%v", req.Method, req.URL.String(), err)

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
	rp.ServeHTTP(w, r)
}

func (s *HttpServer) handleWebsocket(w http.ResponseWriter, r *http.Request, host *file.Host, isHttpOnlyRequest, authed bool) {
	// Get target addr
	targetAddr, err := host.Target.GetRandomTarget()
	if err != nil {
		logs.Warn("No backend found for host: %s Err: %v", r.Host, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	logs.Info("%s websocket request, method %s, host %s, url %s, remote address %s, target %s", r.URL.Scheme, r.Method, r.Host, r.URL.Path, r.RemoteAddr, targetAddr)
	isLocal := s.AllowLocalProxy && host.Target.LocalProxy || host.Client.Id < 0
	link := conn.NewLink("tcp", targetAddr, host.Client.Cnf.Crypt, host.Client.Cnf.Compress, r.RemoteAddr, isLocal)
	targetConn, err := s.Bridge.SendLinkInfo(host.Client.Id, link, nil)
	if err != nil {
		logs.Info("handleWebsocket: connection to target %s failed: %v", link.Host, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	rawConn := conn.GetConn(targetConn, link.Crypt, link.Compress, host.Client.Rate, true, isLocal, host.RateLimiter())
	// Every path below closes netConn, which wraps wsConn, or hands it to the relay.
	//noinspection GoResourceLeak
	wsConn := conn.NewRWConn(rawConn)
	var netConn net.Conn = wsConn

	if host.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", r.RemoteAddr)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		if hdr := conn.ProxyProtocolHeader(host.Target.ProxyProtocol, ra, la); hdr != nil {
			_ = netConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := hdr.WriteTo(netConn); err != nil {
				_ = netConn.Close()
				return
			}
			_ = netConn.SetWriteDeadline(time.Time{})
		}
	}

	if host.TargetIsHttps {
		sni, _ := r.Context().Value(ctxSNI).(string)
		tlsConn, err := conn.GetTlsConn(netConn, sni)
		if err != nil {
			_ = netConn.Close()
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		netConn = tlsConn
	}

	s.ChangeHostAndHeader(r, host.HostChange, host.HeaderChange, isHttpOnlyRequest || host.CompatMode)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack not supported")
		return
	}

	r.Close = false
	if err := r.Write(netConn); err != nil {
		logs.Error("handleWebsocket: failed to write handshake to backend: %v", err)
		_ = netConn.Close()
		s.writeErrorPage(w, http.StatusBadGateway, s.ErrorContent)
		return
	}

	backendReader := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(backendReader, r)
	if err != nil {
		logs.Error("handleWebsocket: failed to read handshake response from backend: %v", err)
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
		logs.Info("handleWebsocket: backend answered handshake with status %d, host %s, remote address %s", resp.StatusCode, r.Host, r.RemoteAddr)
		if !authed {
			// An unauthenticated client never sees a backend response.
			_ = netConn.Close()
			logs.Warn("Unauthorized request from %s", r.RemoteAddr)
			writeUnauthorized(w)
			return
		}
		s.relayHandshakeRefusal(w, resp, host, netConn)
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack failed: %v", err)
		return
	}

	if err := resp.Write(clientBuf); err != nil {
		logs.Error("handleWebsocket: failed to write handshake response to client: %v", err)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}
	if err := clientBuf.Flush(); err != nil {
		logs.Error("handleWebsocket: failed to flush handshake response to client: %v", err)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}

	if backendReader.Buffered() > 0 {
		pending := make([]byte, backendReader.Buffered())
		if _, err := backendReader.Read(pending); err == nil {
			netConn = conn.NewConn(netConn).SetRb(pending)
		} else {
			logs.Error("handleWebsocket: read backend buffered data failed: %v", err)
			_ = netConn.Close()
			_ = clientConn.Close()
			return
		}
	}

	bufReader := clientBuf.Reader
	if bufReader.Buffered() > 0 {
		pending := make([]byte, bufReader.Buffered())
		if _, err := bufReader.Read(pending); err != nil {
			logs.Error("handleWebsocket: failed to read buffered data from client: %v", err)
			_ = netConn.Close()
			_ = clientConn.Close()
			return
		}
		clientConn = conn.NewConn(clientConn).SetRb(pending)
	}

	conn.Join(clientConn, netConn, []*file.Flow{host.Flow, host.Client.Flow}, s.Task, r.RemoteAddr)
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
func (s *HttpServer) relayHandshakeRefusal(w http.ResponseWriter, resp *http.Response, host *file.Host, backend net.Conn) {
	defer func() { _ = backend.Close() }()
	if s.ResponseHeaderTimeout > 0 {
		timer := time.AfterFunc(s.ResponseHeaderTimeout, func() { _ = backend.Close() })
		defer timer.Stop()
	}
	s.writeHandshakeRefusal(w, resp, host)
}

// writeHandshakeRefusal relays a backend response that did not complete an
// upgrade or CONNECT handshake as an ordinary HTTP response and asks the
// server to close the client connection afterwards.
func (s *HttpServer) writeHandshakeRefusal(w http.ResponseWriter, resp *http.Response, host *file.Host) {
	if resp.StatusCode < http.StatusOK {
		s.writeErrorPage(w, http.StatusBadGateway, s.ErrorContent)
		return
	}
	s.ChangeResponseHeader(resp, host.RespHeaderChange)
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
		// Disable HTTP/2.
		//TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
}

func (s *HttpServer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	//logs.Debug("DialContext: start dialing; network=%s, addr=%s, using targetAddr=%s", network, addr, targetAddr)
	remote := ctx.Value(ctxRemoteAddr).(string)
	h := ctx.Value(ctxHost).(*file.Host)
	targetAddr, err := h.Target.GetRandomTarget()
	if err != nil {
		logs.Warn("No backend found for h: %d Err: %v", h.Id, err)
		return nil, err
	}
	isLocal := s.AllowLocalProxy && h.Target.LocalProxy || h.Client.Id < 0
	link := conn.NewLink("tcp", targetAddr, h.Client.Cnf.Crypt, h.Client.Cnf.Compress, remote, isLocal)
	target, err := s.Bridge.SendLinkInfo(h.Client.Id, link, nil)
	if err != nil {
		logs.Info("DialContext: connection to host %d (target %s) failed: %v", h.Id, targetAddr, err)
		return nil, err
	}
	rawConn := conn.GetConn(target, link.Crypt, link.Compress, h.Client.Rate, true, isLocal, h.RateLimiter())
	flowConn := conn.NewFlowConn(rawConn, h.Flow, h.Client.Flow)
	if h.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", remote)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := ctx.Value(http.LocalAddrContextKey).(*net.TCPAddr)
		if hdr := conn.ProxyProtocolHeader(h.Target.ProxyProtocol, ra, la); hdr != nil {
			_ = flowConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := hdr.WriteTo(flowConn); err != nil {
				_ = flowConn.Close()
				return nil, fmt.Errorf("write PROXY header: %w", err)
			}
			_ = flowConn.SetWriteDeadline(time.Time{})
		}
	}
	return flowConn, nil
}

func (s *HttpServer) DialTlsContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := s.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	sni := ctx.Value(ctxSNI).(string)

	tlsConn, err := conn.GetTlsConn(c, sni)
	if err != nil {
		_ = c.Close()
		return nil, err
	}

	return tlsConn, nil
}
