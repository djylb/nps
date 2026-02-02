package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"

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

func (s *TunnelModeServer) Start() error {
	if s.Task.ServerIp == "" {
		s.Task.ServerIp = "0.0.0.0"
	}
	s.address = common.BuildAddress(s.Task.ServerIp, strconv.Itoa(s.Task.Port))
	return conn.NewTcpListenerAndProcess(s.address, s.handleConn, &s.listener)
}

func (s *TunnelModeServer) Close() error {
	s.activeConnections.Range(func(key, value interface{}) bool {
		if c, ok := key.(net.Conn); ok {
			_ = c.Close()
			s.activeConnections.Delete(key)
		}
		return true
	})
	s.activeConnections = sync.Map{}
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *TunnelModeServer) ServeVirtual(c net.Conn) {
	if c != nil {
		go s.handleConn(c)
	}
}

func (s *TunnelModeServer) DialVirtual(rAddr string) (net.Conn, error) {
	a, b := net.Pipe()
	c, err := conn.NewAddrOverrideConn(b, rAddr, s.address)
	if err != nil {
		return nil, err
	}
	go s.handleConn(c)
	return a, nil
}

func (s *TunnelModeServer) handleConn(c net.Conn) {
	if c == nil {
		return
	}
	s.activeConnections.Store(c, struct{}{})
	defer func() {
		s.activeConnections.Delete(c)
		if c != nil {
			_ = c.Close()
		}
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

	return s.DealClient(c, s.Task.Client, targetAddr, nil, common.CONN_TCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
}

// ProcessHttp http proxy
func ProcessHttp(c *conn.Conn, s *TunnelModeServer) error {
	_, addr, rb, err, r := c.GetHost()
	if err != nil {
		_ = c.Close()
		logs.Info("%v", err)
		return err
	}
	if err := s.Auth(r, nil, s.Task.Client.Cnf.U, s.Task.Client.Cnf.P, s.Task.MultiAccount, s.Task.UserAuth); err != nil {
		_, _ = c.Write([]byte(common.ProxyAuthRequiredBytes))
		_ = c.Close()
		return err
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
		return s.DealClient(c, s.Task.Client, addr, nil, common.CONN_TCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, s.Task.Target.LocalProxy, s.Task)
	}
	var server *http.Server

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			//req.RequestURI = ""
			//req.Header.Del("Proxy-Connection")
			//req.Header.Del("Proxy-Authenticate")
			//req.Header.Del("Proxy-Authorization")
			//req.Header.Del("TE")
			//req.Header.Del("Trailers")
			//req.Header.Del("Transfer-Encoding")
			//req.Header.Del("Upgrade")
			//connections := req.Header.Get("Connection")
			//req.Header.Del("Connection")
			//if connections != "" {
			//	for _, h := range strings.Split(connections, ",") {
			//		req.Header.Del(strings.TrimSpace(h))
			//	}
			//}
			req.Header["X-Forwarded-For"] = nil
		},
		Transport: &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
			//DisableKeepAlives:     true,
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
				rawConn := conn.GetConn(target, link.Crypt, link.Compress, s.Task.Client.Rate, true, isLocal)
				return conn.NewFlowConn(rawConn, s.Task.Flow, s.Task.Client.Flow), nil
			},
		},
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
	listener := conn.NewOneConnListener(c.SetRb(rb))
	var shutdownTimerMu sync.Mutex
	var shutdownTimer *time.Timer
	defer func() {
		_ = listener.Close()
		shutdownTimerMu.Lock()
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
		shutdownTimerMu.Unlock()
	}()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		shutdownTimerMu.Lock()
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
		shutdownTimerMu.Unlock()
		defer func() {
			//logs.Error("HTTP Proxy Number: %d, Reset timeout", httpNum)
			shutdownTimerMu.Lock()
			shutdownTimer = time.AfterFunc(30*time.Second, func() {
				_ = server.Close()
			})
			shutdownTimerMu.Unlock()
		}()
		proxy.ServeHTTP(w, req)
	})
	server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	//httpNum++
	//logs.Error("HTTP Proxy Number: %d", httpNum)
	//defer func() {
	//	httpNum--
	//	logs.Error("HTTP Proxy Number: %d", httpNum)
	//}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	//logs.Error("HTTP Proxy Close")
	return nil
}
