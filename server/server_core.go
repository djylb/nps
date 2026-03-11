package server

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
	"github.com/djylb/nps/server/proxy/httpproxy"
	"github.com/djylb/nps/server/tool"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

var (
	Bridge         *bridge.Bridge
	RunList        sync.Map //map[int]interface{}
	once           sync.Once
	HttpProxyCache = index.NewAnyIntIndex()
)

func init() {
	RunList = sync.Map{}
	tool.SetLookup(func(id int) (tool.Dialer, bool) {
		if v, ok := RunList.Load(id); ok {
			if svr, ok := v.(*proxy.TunnelModeServer); ok {
				if !strings.Contains(svr.Task.Target.TargetStr, "tunnel://") {
					return svr, true
				}
			}
		}
		return nil, false
	})
}

// InitFromDb init task from db
func InitFromDb() {
	if allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy"); allowLocalProxy {
		db := file.GetDb()
		if _, err := db.GetClient(-1); err != nil {
			local := new(file.Client)
			local.Id = -1
			local.Remark = "Local Proxy"
			local.Addr = "127.0.0.1"
			local.Cnf = new(file.Config)
			local.Flow = new(file.Flow)
			local.Rate = rate.NewRate(0)
			local.Rate.Start()
			local.NowConn = 0
			local.Status = true
			local.ConfigConnAllow = true
			local.Version = version.VERSION
			local.VerifyKey = "localproxy"
			db.JsonDb.Clients.Store(local.Id, local)
			logs.Info("Auto create local proxy client.")
		}
	}

	//Add a public password
	if vkey := beego.AppConfig.String("public_vkey"); vkey != "" {
		c := file.NewClient(vkey, true, true)
		_ = file.GetDb().NewClient(c)
		RunList.Store(c.Id, nil)
		//RunList[c.Id] = nil
	}
	//Initialize services in server-side files
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if value.(*file.Tunnel).Status {
			_ = AddTask(value.(*file.Tunnel))
		}
		return true
	})
}

// DealBridgeTask get bridge command
func DealBridgeTask() {
	for {
		select {
		case h := <-Bridge.OpenHost:
			if h != nil {
				HttpProxyCache.Remove(h.Id)
			}
		case t := <-Bridge.OpenTask:
			if t != nil {
				//_ = AddTask(t)
				_ = StopServer(t.Id)
				if err := StartTask(t.Id); err != nil {
					logs.Error("StartTask(%d) error: %v", t.Id, err)
				}
			}
		case t := <-Bridge.CloseTask:
			if t != nil {
				_ = StopServer(t.Id)
			}
		case id := <-Bridge.CloseClient:
			DelTunnelAndHostByClientId(id, true)
			if v, ok := file.GetDb().JsonDb.Clients.Load(id); ok {
				if v.(*file.Client).NoStore {
					_ = file.GetDb().DelClient(id)
				}
			}
		//case tunnel := <-Bridge.OpenTask:
		//	_ = StartTask(tunnel.Id)
		case s := <-Bridge.SecretChan:
			if s != nil {
				logs.Trace("New secret connection, addr %v", s.Conn.Conn.RemoteAddr())
				if t := file.GetDb().GetTaskByMd5Password(s.Password); t != nil {
					if t.Status {
						allowLocalProxy := beego.AppConfig.DefaultBool("allow_local_proxy", false)
						allowSecretLink := beego.AppConfig.DefaultBool("allow_secret_link", false)
						allowSecretLocal := beego.AppConfig.DefaultBool("allow_secret_local", false)
						go func() {
							_ = proxy.NewSecretServer(Bridge, t, allowLocalProxy, allowSecretLink, allowSecretLocal).HandleSecret(s.Conn)
						}()
					} else {
						_ = s.Conn.Close()
						logs.Trace("This key %s cannot be processed,status is close", s.Password)
					}
				} else {
					logs.Trace("This key %s cannot be processed", s.Password)
					_ = s.Conn.Close()
				}
			}
		}
	}
}

// StartNewServer start a new server
func StartNewServer(cnf *file.Tunnel, bridgeDisconnect int) {
	Bridge = bridge.NewTunnel(common.GetBoolByStr(beego.AppConfig.String("ip_limit")), &RunList, bridgeDisconnect)
	go func() {
		if err := Bridge.StartTunnel(); err != nil {
			logs.Error("start server bridge error %v", err)
			os.Exit(1)
		}
	}()
	if p, err := beego.AppConfig.Int("p2p_port"); err == nil {
		for i := 0; i < 3; i++ {
			port := p + i
			if common.TestUdpPort(port) {
				go func(pp int) { _ = proxy.NewP2PServer(pp).Start() }(port)
				logs.Info("Started P2P Server on port %d", port)
			} else {
				logs.Error("Port %d is unavailable.", port)
			}
		}
	}
	go DealBridgeTask()
	go dealClientFlow()
	InitDashboardData()
	if svr := NewMode(Bridge, cnf); svr != nil {
		if err := svr.Start(); err != nil {
			logs.Error("%v", err)
		}
		RunList.Store(cnf.Id, svr)
		//RunList[cnf.Id] = svr
	} else {
		logs.Error("Incorrect startup mode %s", cnf.Mode)
	}
}

func dealClientFlow() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		dealClientData()
	}
}

const pingTimeout = 15 * time.Second

func PingClient(id int, addr string) int {
	if id <= 0 {
		return 0
	}
	link := conn.NewLink("ping", "", false, false, addr, false)
	link.Option.NeedAck = true
	link.Option.Timeout = pingTimeout
	start := time.Now()
	target, err := Bridge.SendLinkInfo(id, link, nil)
	if err != nil {
		logs.Warn("get connection from client Id %d error %v", id, err)
		return -1
	}
	rtt := int(time.Since(start).Milliseconds())
	_ = target.Close()
	return rtt
}

// NewMode new a server by mode name
func NewMode(Bridge *bridge.Bridge, c *file.Tunnel) proxy.Service {
	var service proxy.Service
	allowLocalProxy := beego.AppConfig.DefaultBool("allow_local_proxy", false)
	switch c.Mode {
	case "tcp", "file":
		service = proxy.NewTunnelModeServer(proxy.ProcessTunnel, Bridge, c, allowLocalProxy)
	case "mixProxy", "socks5", "httpProxy":
		service = proxy.NewTunnelModeServer(proxy.ProcessMix, Bridge, c, allowLocalProxy)
		//service = proxy.NewSock5ModeServer(Bridge, c)
		//service = proxy.NewTunnelModeServer(proxy.ProcessHttp, Bridge, c)
	case "tcpTrans":
		service = proxy.NewTunnelModeServer(proxy.HandleTrans, Bridge, c, allowLocalProxy)
	case "udp":
		service = proxy.NewUdpModeServer(Bridge, c, allowLocalProxy)
	case "webServer":
		InitFromDb()
		t := &file.Tunnel{
			Port:   0,
			Mode:   "httpHostServer",
			Status: true,
		}
		_ = AddTask(t)
		service = NewWebServer(Bridge)
	case "httpHostServer":
		httpPort := connection.HttpPort
		httpsPort := connection.HttpsPort
		http3Port := connection.Http3Port
		//useCache, _ := beego.AppConfig.Bool("http_cache")
		//cacheLen, _ := beego.AppConfig.Int("http_cache_length")
		addOrigin, _ := beego.AppConfig.Bool("http_add_origin_header")
		httpOnlyPass := beego.AppConfig.String("x_nps_http_only")
		service = httpproxy.NewHttpProxy(Bridge, c, httpPort, httpsPort, http3Port, httpOnlyPass, addOrigin, allowLocalProxy, HttpProxyCache)
	}
	return service
}

// StopServer stop server
func StopServer(id int) error {
	if t, err := file.GetDb().GetTask(id); err != nil {
		return err
	} else {
		t.Status = false
		logs.Info("close port %d,remark %s,client id %d,task id %d", t.Port, t.Remark, t.Client.Id, t.Id)
		_ = file.GetDb().UpdateTask(t)
	}
	//if v, ok := RunList[id]; ok {
	if v, ok := RunList.Load(id); ok {
		if svr, ok := v.(proxy.Service); ok {
			if err := svr.Close(); err != nil {
				return err
			}
			logs.Info("stop server id %d", id)
		} else {
			logs.Warn("stop server id %d error", id)
		}
		//delete(RunList, id)
		RunList.Delete(id)
		return nil
	}
	return errors.New("task is not running")
}

// AddTask add task
func AddTask(t *file.Tunnel) error {
	if t.Mode == "secret" || t.Mode == "p2p" {
		logs.Info("secret task %s start ", t.Remark)
		//RunList[t.Id] = nil
		RunList.Store(t.Id, nil)
		return nil
	}
	if b := tool.TestServerPort(t.Port, t.Mode); !b && t.Mode != "httpHostServer" {
		logs.Error("taskId %d start error port %d open failed", t.Id, t.Port)
		return errors.New("the port open error")
	}
	if minute, err := beego.AppConfig.Int("flow_store_interval"); err == nil && minute > 0 {
		go flowSession(time.Minute * time.Duration(minute))
	}
	if svr := NewMode(Bridge, t); svr != nil {
		logs.Info("tunnel task %s start mode：%s port %d", t.Remark, t.Mode, t.Port)
		//RunList[t.Id] = svr
		RunList.Store(t.Id, svr)
		go func() {
			if err := svr.Start(); err != nil {
				logs.Error("clientId %d taskId %d start error %v", t.Client.Id, t.Id, err)
				//delete(RunList, t.Id)
				RunList.Delete(t.Id)
				return
			}
		}()
	} else {
		return errors.New("the mode is not correct")
	}
	return nil
}

// StartTask start task
func StartTask(id int) error {
	if t, err := file.GetDb().GetTask(id); err != nil {
		return err
	} else {
		if !tool.TestServerPort(t.Port, t.Mode) {
			return errors.New("the port open error")
		}
		err = AddTask(t)
		if err != nil {
			return err
		}
		t.Status = true
		_ = file.GetDb().UpdateTask(t)
	}
	return nil
}

// DelTask delete task
func DelTask(id int) error {
	//if _, ok := RunList[id]; ok {
	if _, ok := RunList.Load(id); ok {
		if err := StopServer(id); err != nil {
			return err
		}
	}
	return file.GetDb().DelTask(id)
}
