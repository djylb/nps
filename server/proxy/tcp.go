package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type TunnelModeServer struct {
	*BaseServer
	process           process
	address           string
	listener          net.Listener
	socks5UDP         *socks5UDPRegistry
	activeConnections sync.Map
}

// NewTunnelModeServer tcp|host|mixproxy
func NewTunnelModeServer(process process, bridge NetBridge, task *file.Tunnel, allowLocalProxy bool) *TunnelModeServer {
	return &TunnelModeServer{
		BaseServer:        NewBaseServer(bridge, task, allowLocalProxy),
		process:           process,
		activeConnections: sync.Map{},
	}
}

// Bind opens the TCP listener and, for SOCKS5, the UDP associate socket.
func (s *TunnelModeServer) Bind() error {
	return s.bindLocked(func() error {
		if s.listener != nil {
			return nil
		}
		if s.Task.ServerIp == "" {
			s.Task.ServerIp = "0.0.0.0"
		}
		s.address = common.BuildAddress(s.Task.ServerIp, strconv.Itoa(s.Task.Port))

		var registry *socks5UDPRegistry
		if s.usesSocks5UDP() {
			var err error
			if registry, err = newSocks5UDPRegistry(s.Bridge, s.Task, s.AllowLocalProxy, s.address); err != nil {
				return err
			}
		}
		listener, err := net.Listen("tcp", s.address)
		if err != nil {
			if registry != nil {
				_ = registry.Close()
			}
			return err
		}
		if registry != nil {
			registry.start()
		}
		s.socks5UDP = registry
		s.listener = listener
		return nil
	})
}

func (s *TunnelModeServer) Start() error {
	if err := s.Bind(); err != nil {
		return err
	}
	conn.Accept(s.listener, s.handleConn)
	if s.socks5UDP != nil {
		_ = s.socks5UDP.Close()
	}
	return nil
}

func (s *TunnelModeServer) Close() error {
	var listener net.Listener
	var registry *socks5UDPRegistry
	s.markClosed(func() {
		listener = s.listener
		registry = s.socks5UDP
	})
	s.activeConnections.Range(func(key, value interface{}) bool {
		if c, ok := key.(net.Conn); ok {
			_ = c.Close()
			s.activeConnections.Delete(key)
		}
		return true
	})
	if registry != nil {
		_ = registry.Close()
	}
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// trackConn registers c as active unless the server is closed, so Close
// never misses a connection accepted concurrently with it.
func (s *TunnelModeServer) trackConn(c net.Conn) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return false
	}
	s.activeConnections.Store(c, struct{}{})
	return true
}

func (s *TunnelModeServer) ServeVirtual(c net.Conn) {
	if c != nil {
		go s.handleConn(c)
	}
}

func (s *TunnelModeServer) DialVirtual(rAddr string) (net.Conn, error) {
	remote, err := conn.ParseTCPAddr(rAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid remote addr %q: %w", rAddr, err)
	}
	local, _ := conn.ParseTCPAddr(s.address)
	a, b := net.Pipe()
	// handleConn owns the server end and closes it.
	//noinspection GoResourceLeak
	go s.handleConn(netx.NewAddrOverrideConn(b, local, remote))
	return a, nil
}

func (s *TunnelModeServer) handleConn(c net.Conn) {
	if c == nil {
		return
	}
	if !s.trackConn(c) {
		_ = c.Close()
		return
	}
	defer func() {
		s.activeConnections.Delete(c)
		_ = c.Close()
	}()

	if s.Bridge.IsServer() {
		if err := s.CheckFlowAndConnNum(s.Task.Client); err != nil {
			logs.Warn("client Id %d, task Id %d, error %v, when tcp connection", s.Task.Client.Id, s.Task.Id, err)
			_ = c.Close()
			return
		}
		defer s.Task.Client.CutConn()
		s.Task.AddConn()
		defer s.Task.CutConn()
	}
	logs.Trace("new tcp connection,local port %d,client %d,remote address %v", s.Task.Port, s.Task.Client.Id, c.RemoteAddr())

	_ = s.process(conn.NewConn(c), s)
}

func (s *TunnelModeServer) usesSocks5UDP() bool {
	if s == nil || s.Task == nil {
		return false
	}
	_, socksEnabled := effectiveMixProxyFlags(s.Task)
	return socksEnabled
}

type process func(c *conn.Conn, s *TunnelModeServer) error

// ProcessTunnel tcp proxy
func ProcessTunnel(c *conn.Conn, s *TunnelModeServer) error {
	targetAddr, err := s.Task.Target.GetRandomTarget()
	if err != nil {
		if s.Task.Mode != "file" && s.Bridge.IsServer() {
			_ = c.Close()
			logs.Warn("tcp port %d, client Id %d, task Id %d connect error %v", s.Task.Port, s.Task.Client.Id, s.Task.Id, err)
			return err
		}
		targetAddr = ""
	}

	return s.DealClient(c, s.Task.Client, targetAddr, nil, common.ConnTCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
}

// httpProxyHeaderTimeout bounds how long a client may take to send a request header.
const httpProxyHeaderTimeout = 10 * time.Second

// ProcessHttp http proxy
func ProcessHttp(c *conn.Conn, s *TunnelModeServer) error {
	if err := c.SetReadDeadline(time.Now().Add(httpProxyHeaderTimeout)); err != nil {
		_ = c.Close()
		return err
	}
	addr, r, head, rest, err := c.GetHost()
	if err == nil {
		err = c.SetReadDeadline(time.Time{})
	}
	if err != nil {
		_ = c.Close()
		logs.Info("%v", err)
		return err
	}
	if !common.CheckProxyAuth(r, s.Task.Client.Cnf.U, s.Task.Client.Cnf.P, file.GetAccountMap(s.Task.MultiAccount), file.GetAccountMap(s.Task.UserAuth)) {
		_, _ = c.Write([]byte(common.ProxyAuthRequiredBytes))
		_ = c.Close()
		return errors.New("407 Proxy Authentication Required")
	}
	if s.Task != nil && s.Task.Mode == "mixProxy" && s.Task.DestAclMode != file.AclOff {
		if !s.Task.AllowsDestination(addr) {
			logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s", s.Task.Client.Id, s.Task.Id, common.ExtractHost(addr))
			_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			_ = c.Close()
			return errors.New("destination denied by dest acl")
		}
	}
	remoteAddr := c.Conn.RemoteAddr().String()
	logs.Debug("http proxy request, client=%d method=%s, host=%s, url=%s, remote address=%s, target=%s", s.Task.Client.Id, r.Method, r.Host, r.URL.RequestURI(), remoteAddr, addr)
	if r.Method == http.MethodConnect {
		_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		// Bytes the client sent right after the CONNECT header belong to the tunnel.
		return s.DealClient(c, s.Task.Client, addr, rest, common.ConnTCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, s.Task.Target.LocalProxy, s.Task)
	}
	var server *http.Server

	transport := &http.Transport{
		ResponseHeaderTimeout: 100 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if s.Task != nil && s.Task.Mode == "mixProxy" && s.Task.DestAclMode != file.AclOff {
				if !s.Task.AllowsDestination(addr) {
					logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s", s.Task.Client.Id, s.Task.Id, common.ExtractHost(addr))
					return nil, errors.New("destination denied by dest acl")
				}
			}
			isLocal := s.AllowLocalProxy && s.Task.Target.LocalProxy || s.Task.Client.Id < 0
			link := conn.NewLink("tcp", addr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, remoteAddr, isLocal)
			target, err := s.Bridge.SendLinkInfo(s.Task.Client.Id, link, nil)
			if err != nil {
				logs.Trace("DialContext: connection to host %s (target %s) failed: %v", r.Host, addr, err)
				return nil, err
			}
			rawConn := conn.GetConn(target, link.Crypt, link.Compress, s.Task.Client.Rate, true, isLocal, s.Task.RateLimiter())
			return conn.NewFlowConn(rawConn, s.Task.Flow, s.Task.Client.Flow), nil
		},
	}
	defer transport.CloseIdleConnections()

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The client's forwarding headers pass through, except
			// X-Forwarded-For, which is neither relayed nor added.
			KeepClientForwarding(pr)
			pr.Out.Header.Del("X-Forwarded-For")
		},
		Transport: transport,
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			if err == io.EOF {
				//logs.Info("ErrorHandler: io.EOF encountered, writing 521")
				rw.WriteHeader(521)
				//go server.Close()
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
				rw.WriteHeader(http.StatusBadGateway)
			}
			//go server.Close()
		},
	}
	// Replay the parsed header and everything read after it (body, pipelined
	// requests). Serve returns once this single connection is closed.
	listener := netx.NewSingleConnListener(c.SetRb(head, rest))
	// The idle timer closes the server once no request has arrived for a
	// while; it is never armed after ProcessHttp has returned.
	var idleMu sync.Mutex
	var idleTimer *time.Timer
	done := false
	defer func() {
		_ = listener.Close()
		idleMu.Lock()
		done = true
		if idleTimer != nil {
			idleTimer.Stop()
		}
		idleMu.Unlock()
	}()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		idleMu.Lock()
		if idleTimer != nil {
			idleTimer.Stop()
		}
		idleMu.Unlock()
		defer func() {
			idleMu.Lock()
			if !done {
				idleTimer = time.AfterFunc(30*time.Second, func() {
					_ = server.Close()
				})
			}
			idleMu.Unlock()
		}()
		proxy.ServeHTTP(w, req)
	})
	server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: httpProxyHeaderTimeout,
		IdleTimeout:       90 * time.Second,
	}
	//httpNum++
	//logs.Error("HTTP Proxy Number: %d", httpNum)
	//defer func() {
	//	httpNum--
	//	logs.Error("HTTP Proxy Number: %d", httpNum)
	//}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	//logs.Error("HTTP Proxy Close")
	return nil
}
