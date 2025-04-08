package proxy

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"

	"github.com/beego/beego"
	"github.com/beego/beego/logs"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server/connection"
)

var _ = unsafe.Sizeof(0)

//go:linkname initBeforeHTTPRun github.com/beego/beego.initBeforeHTTPRun
func initBeforeHTTPRun()

type TunnelModeServer struct {
	BaseServer
	process           process
	listener          net.Listener
	activeConnections sync.Map
}

// tcp|http|host
func NewTunnelModeServer(process process, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy")
	s := new(TunnelModeServer)
	s.bridge = bridge
	s.process = process
	s.task = task
	s.allowLocalProxy = allowLocalProxy
	s.activeConnections = sync.Map{} // 初始化连接池
	return s
}

// 开始
func (s *TunnelModeServer) Start() error {
	return conn.NewTcpListenerAndProcess(s.task.ServerIp+":"+strconv.Itoa(s.task.Port), func(c net.Conn) {
		// 将新连接加入到连接池中
		s.activeConnections.Store(c, struct{}{})

		defer func() {
			// 从连接池中移除连接
			s.activeConnections.Delete(c)

			if c != nil {
				c.Close()
			}
		}()

		if err := s.CheckFlowAndConnNum(s.task.Client); err != nil {
			logs.Warn("client id %d, task id %d,error %s, when tcp connection", s.task.Client.Id, s.task.Id, err.Error())
			c.Close()
			return
		}

		logs.Trace("new tcp connection,local port %d,client %d,remote address %s", s.task.Port, s.task.Client.Id, c.RemoteAddr())

		s.process(conn.NewConn(c), s)
		s.task.Client.CutConn()
	}, &s.listener)
}

// Close 停止服务器并关闭所有连接
func (s *TunnelModeServer) Close() error {
	// 遍历连接池中的所有连接并关闭它们
	s.activeConnections.Range(func(key, value interface{}) bool {
		if conn, ok := key.(net.Conn); ok {
			conn.Close()
		}
		return true
	})

	// 关闭监听器
	return s.listener.Close()
}

// web管理方式
type WebServer struct {
	BaseServer
}

// 开始
func (s *WebServer) Start() error {
	p, _ := beego.AppConfig.Int("web_port")
	if p == 0 {
		stop := make(chan struct{})
		<-stop
	}
	beego.BConfig.WebConfig.Session.SessionOn = true
	beego.SetStaticPath(beego.AppConfig.String("web_base_url")+"/static", filepath.Join(common.GetRunPath(), "web", "static"))
	beego.SetViewsPath(filepath.Join(common.GetRunPath(), "web", "views"))
	err := errors.New("Web management startup failure ")
	var l net.Listener
	if l, err = connection.GetWebManagerListener(); err == nil {
		initBeforeHTTPRun()
		if beego.AppConfig.String("web_open_ssl") == "true" {
			keyPath := beego.AppConfig.String("web_key_file")
			certPath := beego.AppConfig.String("web_cert_file")
			err = http.ServeTLS(l, beego.BeeApp.Handlers, certPath, keyPath)
		} else {
			err = http.Serve(l, beego.BeeApp.Handlers)
		}
	} else {
		logs.Error(err)
	}
	return err
}

func (s *WebServer) Close() error {
	return nil
}

// new
func NewWebServer(bridge *bridge.Bridge) *WebServer {
	s := new(WebServer)
	s.bridge = bridge
	return s
}

type process func(c *conn.Conn, s *TunnelModeServer) error

// tcp proxy
func ProcessTunnel(c *conn.Conn, s *TunnelModeServer) error {
	targetAddr, err := s.task.Target.GetRandomTarget()
	if err != nil {
		if s.task.Mode != "file" {
			c.Close()
			logs.Warn("tcp port %d, client id %d, task id %d connect error %s", s.task.Port, s.task.Client.Id, s.task.Id, err.Error())
			return err
		}
		targetAddr = ""
	}

	return s.DealClient(c, s.task.Client, targetAddr, nil, common.CONN_TCP, nil, []*file.Flow{s.task.Flow, s.task.Client.Flow}, s.task.Target.ProxyProtocol, s.task.Target.LocalProxy, s.task)
}

// http proxy
func ProcessHttp(c *conn.Conn, s *TunnelModeServer) error {
	_, addr, rb, err, r := c.GetHost()
	if err != nil {
		c.Close()
		logs.Info(err)
		return err
	}

	if err := s.auth(r, nil, s.task.Client.Cnf.U, s.task.Client.Cnf.P, s.task); err != nil {
		c.Write([]byte(common.ProxyAuthRequiredBytes))
		c.Close()
		return err
	}

	if r.Method == "CONNECT" {
		c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		rb = nil
	}

	return s.DealClient(c, s.task.Client, addr, rb, common.CONN_TCP, nil, []*file.Flow{s.task.Flow, s.task.Client.Flow}, s.task.Target.ProxyProtocol, s.task.Target.LocalProxy, s.task)
}
