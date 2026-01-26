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
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/goroutine"
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

func (s *HttpServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Get host
	host, err := file.GetDb().GetInfoByHost(r.Host, r)
	if err != nil || host.IsClose {
		//http.Error(w, "404 Host not found", http.StatusNotFound)
		if s.ErrorAlways {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(s.ErrorContent)
		} else {
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				_ = c.Close()
			}
		}
		logs.Debug("Host not found: %s %s %s", r.URL.Scheme, r.Host, r.RequestURI)
		return
	}

	// IP Black List
	clientIP := common.GetIpByAddr(r.RemoteAddr)
	if proxy.IsGlobalBlackIp(clientIP) || common.IsBlackIp(clientIP, host.Client.VerifyKey, host.Client.BlackIpList) {
		//http.Error(w, "403 Forbidden", http.StatusForbidden)
		logs.Warn("Blocked IP: %s", clientIP)
		if hj, ok := w.(http.Hijacker); ok {
			c, _, _ := hj.Hijack()
			_ = c.Close()
		}
		return
	}

	// AutoSSL
	if host.AutoSSL && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl) {
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

	// HTTP Auth
	if r.Header.Get("Upgrade") == "" {
		if err := s.Auth(r, nil, host.Client.Cnf.U, host.Client.Cnf.P, host.MultiAccount, host.UserAuth); err != nil {
			logs.Warn("Unauthorized request from %s", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// 307 Temporary Redirect
	if host.RedirectURL != "" {
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
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" || r.Header.Get(":protocol") != "" {
		logs.Trace("Handling websocket from %s", r.RemoteAddr)
		s.handleWebsocket(w, r, host, isHttpOnlyRequest)
		return
	}

	var tr *http.Transport
	if v, ok := s.HttpProxyCache.Get(host.Id); ok {
		tr = v.(*http.Transport)
	} else {
		tr = &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
			DisableKeepAlives:     host.CompatMode,
			DialContext:           s.DialContext,
			DialTLSContext:        s.DialTlsContext,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
		}
		s.HttpProxyCache.Add(host.Id, tr)
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			//req = req.WithContext(context.WithValue(req.Context(), "origReq", r))
			if host.TargetIsHttps {
				req.URL.Scheme = "https"
			} else {
				req.URL.Scheme = "http"
			}
			//logs.Debug("Director: set req.URL.Scheme=%s, req.URL.Host=%s", req.URL.Scheme, req.URL.Host)
			s.ChangeHostAndHeader(req, host.HostChange, host.HeaderChange, isHttpOnlyRequest)
			req.URL.Host = r.Host
			if isHttpOnlyRequest {
				req.Header["X-Forwarded-For"] = nil
			}
		},
		Transport: tr,
		//FlushInterval: 100 * time.Millisecond,
		BufferPool: &common.CopyBuff,
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
				http.Error(rw, errMsg[idx:], http.StatusTooManyRequests)
			} else {
				//http.Error(rw, "502 Bad Gateway", http.StatusBadGateway)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write(s.ErrorContent)
			}
		},
	}
	rp.ServeHTTP(w, r)
}

func (s *HttpServer) handleWebsocket(w http.ResponseWriter, r *http.Request, host *file.Host, isHttpOnlyRequest bool) {
	// Get target addr
	targetAddr, err := host.Target.GetRandomTarget()
	if err != nil {
		logs.Warn("No backend found for host: %s Err: %v", r.Host, err)
		//http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(s.ErrorContent)
		return
	}

	logs.Info("%s websocket request, method %s, host %s, url %s, remote address %s, target %s", r.URL.Scheme, r.Method, r.Host, r.URL.Path, r.RemoteAddr, targetAddr)
	isLocal := s.AllowLocalProxy && host.Target.LocalProxy || host.Client.Id < 0
	link := conn.NewLink("tcp", targetAddr, host.Client.Cnf.Crypt, host.Client.Cnf.Compress, r.RemoteAddr, isLocal)
	targetConn, err := s.Bridge.SendLinkInfo(host.Client.Id, link, nil)
	if err != nil {
		logs.Info("handleWebsocket: connection to target %s failed: %v", link.Host, err)
		//http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(s.ErrorContent)
		return
	}
	rawConn := conn.GetConn(targetConn, link.Crypt, link.Compress, host.Client.Rate, true, isLocal)
	wsConn := conn.NewRWConn(rawConn)
	var netConn net.Conn = wsConn

	if host.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", r.RemoteAddr)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		hdr := conn.BuildProxyProtocolHeaderByAddr(ra, la, host.Target.ProxyProtocol)
		if hdr != nil {
			_ = netConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := netConn.Write(hdr); err != nil {
				_ = netConn.Close()
				return
			}
			_ = netConn.SetWriteDeadline(time.Time{})
		}
	}

	if host.TargetIsHttps {
		sni := r.Context().Value(ctxSNI).(string)
		netConn, err = conn.GetTlsConn(netConn, sni)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write(s.ErrorContent)
			return
		}
	}

	s.ChangeHostAndHeader(r, host.HostChange, host.HeaderChange, isHttpOnlyRequest || host.CompatMode)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack not supported")
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Hijack failed", http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack failed")
		return
	}
	//defer clientConn.Close()
	r.Close = false
	if err := r.Write(netConn); err != nil {
		logs.Error("handleWebsocket: failed to write handshake to backend: %v", err)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}

	backendReader := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(backendReader, r)
	if err != nil {
		logs.Error("handleWebsocket: failed to read handshake response from backend: %v", err)
		_ = netConn.Close()
		_ = clientConn.Close()
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

	good := (r.Method == http.MethodConnect && resp.StatusCode == http.StatusOK) ||
		(r.Method != http.MethodConnect && resp.StatusCode == http.StatusSwitchingProtocols)
	if !good {
		logs.Error("handleWebsocket: unexpected status code in handshake: %d", resp.StatusCode)
		_ = netConn.Close()
		_ = clientConn.Close()
		return
	}

	goroutine.Join(clientConn, netConn, []*file.Flow{host.Flow, host.Client.Flow}, s.Task, r.RemoteAddr)
}

func (s *HttpServer) NewServer(port int, scheme string) *http.Server {
	return &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = scheme
			s.handleProxy(w, r)
		}),
		// Disable HTTP/2.
		//TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
}

func (s *HttpServer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
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
	rawConn := conn.GetConn(target, link.Crypt, link.Compress, h.Client.Rate, true, isLocal)
	flowConn := conn.NewFlowConn(rawConn, h.Flow, h.Client.Flow)
	if h.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", remote)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := ctx.Value(http.LocalAddrContextKey).(*net.TCPAddr)
		hdr := conn.BuildProxyProtocolHeaderByAddr(ra, la, h.Target.ProxyProtocol)
		if hdr != nil {
			_ = flowConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := flowConn.Write(hdr); err != nil {
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
	c, err = conn.GetTlsConn(c, sni)
	if err != nil {
		return nil, err
	}
	return c, nil
}
