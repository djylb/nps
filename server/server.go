package server

import (
	"errors"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
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
}

// InitFromCsv init task from db
func InitFromCsv() {
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
		case t := <-Bridge.OpenTask:
			_ = AddTask(t)
		case t := <-Bridge.CloseTask:
			_ = StopServer(t.Id)
		case id := <-Bridge.CloseClient:
			DelTunnelAndHostByClientId(id, true)
			if v, ok := file.GetDb().JsonDb.Clients.Load(id); ok {
				if v.(*file.Client).NoStore {
					_ = file.GetDb().DelClient(id)
				}
			}
		case tunnel := <-Bridge.OpenTask:
			_ = StartTask(tunnel.Id)
		case s := <-Bridge.SecretChan:
			logs.Trace("New secret connection, addr %v", s.Conn.Conn.RemoteAddr())
			if t := file.GetDb().GetTaskByMd5Password(s.Password); t != nil {
				if t.Status {
					go func() {
						t.AddConn()
						_ = proxy.NewBaseServer(Bridge, t).DealClient(s.Conn, t.Client, t.Target.TargetStr, nil, common.CONN_TCP, nil, []*file.Flow{t.Flow, t.Client.Flow}, t.Target.ProxyProtocol, t.Target.LocalProxy, t)
						t.CutConn()
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

// StartNewServer start a new server
func StartNewServer(bridgePort int, cnf *file.Tunnel, bridgeType string, bridgeDisconnect int) {
	Bridge = bridge.NewTunnel(bridgePort, bridgeType, common.GetBoolByStr(beego.AppConfig.String("ip_limit")), &RunList, bridgeDisconnect)
	go func() {
		if err := Bridge.StartTunnel(); err != nil {
			logs.Error("start server bridge error %v", err)
			os.Exit(0)
		}
	}()
	if p, err := beego.AppConfig.Int("p2p_port"); err == nil {
		for i := 0; i < 3; i++ {
			port := p + i
			if common.TestUdpPort(port) {
				go proxy.NewP2PServer(port).Start()
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
	for {
		select {
		case <-ticker.C:
			dealClientData()
		}
	}
}

// NewMode new a server by mode name
func NewMode(Bridge *bridge.Bridge, c *file.Tunnel) proxy.Service {
	var service proxy.Service
	switch c.Mode {
	case "tcp", "file":
		service = proxy.NewTunnelModeServer(proxy.ProcessTunnel, Bridge, c)
	case "mixProxy", "socks5", "httpProxy":
		service = proxy.NewTunnelModeServer(proxy.ProcessMix, Bridge, c)
		//service = proxy.NewSock5ModeServer(Bridge, c)
		//service = proxy.NewTunnelModeServer(proxy.ProcessHttp, Bridge, c)
	case "tcpTrans":
		service = proxy.NewTunnelModeServer(proxy.HandleTrans, Bridge, c)
	case "udp":
		service = proxy.NewUdpModeServer(Bridge, c)
	case "webServer":
		InitFromCsv()
		t := &file.Tunnel{
			Port:   0,
			Mode:   "httpHostServer",
			Status: true,
		}
		_ = AddTask(t)
		service = proxy.NewWebServer(Bridge)
	case "httpHostServer":
		httpPort, _ := beego.AppConfig.Int("http_proxy_port")
		httpsPort, _ := beego.AppConfig.Int("https_proxy_port")
		http3Port := beego.AppConfig.DefaultInt("http3_proxy_port", httpsPort)
		//useCache, _ := beego.AppConfig.Bool("http_cache")
		//cacheLen, _ := beego.AppConfig.Int("http_cache_length")
		addOrigin, _ := beego.AppConfig.Bool("http_add_origin_header")
		httpOnlyPass := beego.AppConfig.String("x_nps_http_only")
		service = httpproxy.NewHttp(Bridge, c, httpPort, httpsPort, http3Port, httpOnlyPass, addOrigin, HttpProxyCache)
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
		_ = AddTask(t)
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

// GetTunnel get task list by page num
func GetTunnel(start, length int, typeVal string, clientId int, search string, sortField string, order string) ([]*file.Tunnel, int) {
	allList := make([]*file.Tunnel, 0) //store all Tunnel
	list := make([]*file.Tunnel, 0)
	originLength := length
	var cnt int
	keys := file.GetMapKeys(&file.GetDb().JsonDb.Tasks, false, "", "")

	//get all Tunnel and sort
	for _, key := range keys {
		if value, ok := file.GetDb().JsonDb.Tasks.Load(key); ok {
			v := value.(*file.Tunnel)
			if (typeVal != "" && v.Mode != typeVal || (clientId != 0 && v.Client.Id != clientId)) || (typeVal == "" && clientId != v.Client.Id) {
				continue
			}
			allList = append(allList, v)
		}
	}
	//sort by Id, Remark, TargetStr, Port, asc or desc
	if sortField == "Id" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Id < allList[j].Id })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Id > allList[j].Id })
		}
	} else if sortField == "Client.Id" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.Id < allList[j].Client.Id })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.Id > allList[j].Client.Id })
		}
	} else if sortField == "Remark" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Remark < allList[j].Remark })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Remark > allList[j].Remark })
		}
	} else if sortField == "Client.VerifyKey" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.VerifyKey < allList[j].Client.VerifyKey })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.VerifyKey > allList[j].Client.VerifyKey })
		}
	} else if sortField == "Target.TargetStr" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Target.TargetStr < allList[j].Target.TargetStr })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Target.TargetStr > allList[j].Target.TargetStr })
		}
	} else if sortField == "Port" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Port < allList[j].Port })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Port > allList[j].Port })
		}
	} else if sortField == "Mode" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Mode < allList[j].Mode })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Mode > allList[j].Mode })
		}
	} else if sortField == "Password" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Password < allList[j].Password })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Password > allList[j].Password })
		}
	} else if sortField == "HttpProxy" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].HttpProxy && !allList[j].HttpProxy })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].HttpProxy && allList[j].HttpProxy })
		}
	} else if sortField == "Socks5Proxy" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Socks5Proxy && !allList[j].Socks5Proxy })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Socks5Proxy && allList[j].Socks5Proxy })
		}
	} else if sortField == "NowConn" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn < list[j].NowConn })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn > list[j].NowConn })
		}
	} else if sortField == "InletFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow < list[j].Flow.InletFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow > list[j].Flow.InletFlow })
		}
	} else if sortField == "ExportFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow < list[j].Flow.ExportFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow > list[j].Flow.ExportFlow })
		}
	} else if sortField == "TotalFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow < list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow > list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		}
	} else if sortField == "FlowRemain" {
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(list[i].Flow), rem(list[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})
	} else if sortField == "Flow.FlowLimit" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := list[i].Flow.FlowLimit, list[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.FlowLimit > list[j].Flow.FlowLimit })
		}
	} else if sortField == "Flow.TimeLimit" || sortField == "TimeRemain" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := list[i].Flow.TimeLimit, list[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.TimeLimit.After(list[j].Flow.TimeLimit) })
		}
	} else if sortField == "Status" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Status && !allList[j].Status })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Status && allList[j].Status })
		}
	} else if sortField == "RunStatus" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].RunStatus && !allList[j].RunStatus })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].RunStatus && allList[j].RunStatus })
		}
	} else if sortField == "Client.IsConnect" {
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.IsConnect && !allList[j].Client.IsConnect })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Client.IsConnect && allList[j].Client.IsConnect })
		}
	}

	//search
	for _, key := range allList {
		if value, ok := file.GetDb().JsonDb.Tasks.Load(key.Id); ok {
			v := value.(*file.Tunnel)
			if (typeVal != "" && v.Mode != typeVal || (clientId != 0 && v.Client.Id != clientId)) || (typeVal == "" && clientId != v.Client.Id) {
				continue
			}
			if search != "" && !(v.Id == common.GetIntNoErrByStr(search) || v.Port == common.GetIntNoErrByStr(search) || common.ContainsFold(v.Password, search) || common.ContainsFold(v.Remark, search) || common.ContainsFold(v.Target.TargetStr, search)) {
				continue
			}
			cnt++
			if _, ok := Bridge.Client.Load(v.Client.Id); ok {
				v.Client.IsConnect = true
			} else {
				v.Client.IsConnect = false
			}
			if start--; start < 0 {
				if originLength == 0 {
					if _, ok := RunList.Load(v.Id); ok {
						v.RunStatus = true
					} else {
						v.RunStatus = false
					}
					list = append(list, v)
				} else if length--; length >= 0 {
					//if _, ok := RunList[v.Id]; ok {
					if _, ok := RunList.Load(v.Id); ok {
						v.RunStatus = true
					} else {
						v.RunStatus = false
					}
					list = append(list, v)
				}
			}
		}
	}
	return list, cnt
}

// GetHostList get client list
func GetHostList(start, length, clientId int, search, sortField, order string) (list []*file.Host, cnt int) {
	list, cnt = file.GetDb().GetHost(start, length, clientId, search)
	//sort by Id, Remark..., asc or desc
	if sortField == "Id" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id > list[j].Id })
		}
	} else if sortField == "Client.Id" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.Id < list[j].Client.Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.Id > list[j].Client.Id })
		}
	} else if sortField == "Remark" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark < list[j].Remark })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark > list[j].Remark })
		}
	} else if sortField == "Client.VerifyKey" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.VerifyKey < list[j].Client.VerifyKey })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.VerifyKey > list[j].Client.VerifyKey })
		}
	} else if sortField == "Host" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Host < list[j].Host })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Host > list[j].Host })
		}
	} else if sortField == "Scheme" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Scheme < list[j].Scheme })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Scheme > list[j].Scheme })
		}
	} else if sortField == "TargetIsHttps" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].TargetIsHttps && !list[j].TargetIsHttps })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].TargetIsHttps && list[j].TargetIsHttps })
		}
	} else if sortField == "Target.TargetStr" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Target.TargetStr < list[j].Target.TargetStr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Target.TargetStr > list[j].Target.TargetStr })
		}
	} else if sortField == "Location" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Location < list[j].Location })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Location > list[j].Location })
		}
	} else if sortField == "PathRewrite" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].PathRewrite < list[j].PathRewrite })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].PathRewrite > list[j].PathRewrite })
		}
	} else if sortField == "CertType" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CertType < list[j].CertType })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CertType > list[j].CertType })
		}
	} else if sortField == "AutoSSL" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoSSL && !list[j].AutoSSL })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoSSL && list[j].AutoSSL })
		}
	} else if sortField == "AutoHttps" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoHttps && !list[j].AutoHttps })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoHttps && list[j].AutoHttps })
		}
	} else if sortField == "AutoCORS" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoCORS && !list[j].AutoCORS })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoCORS && list[j].AutoCORS })
		}
	} else if sortField == "CompatMode" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CompatMode && !list[j].CompatMode })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].CompatMode && list[j].CompatMode })
		}
	} else if sortField == "HttpsJustProxy" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].HttpsJustProxy && !list[j].HttpsJustProxy })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].HttpsJustProxy && list[j].HttpsJustProxy })
		}
	} else if sortField == "NowConn" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn < list[j].NowConn })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn > list[j].NowConn })
		}
	} else if sortField == "InletFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow < list[j].Flow.InletFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow > list[j].Flow.InletFlow })
		}
	} else if sortField == "ExportFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow < list[j].Flow.ExportFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow > list[j].Flow.ExportFlow })
		}
	} else if sortField == "TotalFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow < list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow > list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		}
	} else if sortField == "FlowRemain" {
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(list[i].Flow), rem(list[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})
	} else if sortField == "Flow.FlowLimit" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := list[i].Flow.FlowLimit, list[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.FlowLimit > list[j].Flow.FlowLimit })
		}
	} else if sortField == "Flow.TimeLimit" || sortField == "TimeRemain" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := list[i].Flow.TimeLimit, list[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.TimeLimit.After(list[j].Flow.TimeLimit) })
		}
	} else if sortField == "IsClose" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].IsClose && !list[j].IsClose })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].IsClose && list[j].IsClose })
		}
	} else if sortField == "Client.IsConnect" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.IsConnect && !list[j].Client.IsConnect })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].Client.IsConnect && list[j].Client.IsConnect })
		}
	}
	return
}

// GetClientList get client list
func GetClientList(start, length int, search, sortField, order string, clientId int) (list []*file.Client, cnt int) {
	list, cnt = file.GetDb().GetClientList(start, length, search, sortField, order, clientId)
	//sort by Id, Remark, Port..., asc or desc
	if sortField == "Id" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id > list[j].Id })
		}
	} else if sortField == "Addr" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Addr < list[j].Addr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Addr > list[j].Addr })
		}
	} else if sortField == "LocalAddr" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].LocalAddr < list[j].LocalAddr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].LocalAddr > list[j].LocalAddr })
		}
	} else if sortField == "Remark" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark < list[j].Remark })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark > list[j].Remark })
		}
	} else if sortField == "VerifyKey" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].VerifyKey < list[j].VerifyKey })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].VerifyKey > list[j].VerifyKey })
		}
	} else if sortField == "TotalFlow" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow < list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow > list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		}
	} else if sortField == "FlowRemain" {
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(list[i].Flow), rem(list[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})
	} else if sortField == "NowConn" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn < list[j].NowConn })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn > list[j].NowConn })
		}
	} else if sortField == "Version" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Version < list[j].Version })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Version > list[j].Version })
		}
	} else if sortField == "Mode" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Mode < list[j].Mode })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Mode > list[j].Mode })
		}
	} else if sortField == "Rate.NowRate" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Rate.NowRate < list[j].Rate.NowRate })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Rate.NowRate > list[j].Rate.NowRate })
		}
	} else if sortField == "Flow.FlowLimit" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := list[i].Flow.FlowLimit, list[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.FlowLimit > list[j].Flow.FlowLimit })
		}
	} else if sortField == "Flow.TimeLimit" || sortField == "TimeRemain" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := list[i].Flow.TimeLimit, list[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.TimeLimit.After(list[j].Flow.TimeLimit) })
		}
	} else if sortField == "Status" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Status && !list[j].Status })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].Status && list[j].Status })
		}
	} else if sortField == "IsConnect" {
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].IsConnect && !list[j].IsConnect })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].IsConnect && list[j].IsConnect })
		}
	}
	dealClientData()
	return
}

func dealClientData() {
	//logs.Info("dealClientData.........")
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if vv, ok := Bridge.Client.Load(v.Id); ok {
			v.IsConnect = true
			v.LastOnlineTime = time.Now().Format("2006-01-02 15:04:05")
			v.Version = vv.(*bridge.Client).Version
		} else if v.Id <= 0 {
			if allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy"); allowLocalProxy {
				v.IsConnect = v.Status
				v.Version = version.VERSION
				v.Mode = "local"
				v.LocalAddr = common.GetOutboundIP().String()
				// Add Local Client
				if _, exists := Bridge.Client.Load(v.Id); !exists && v.Status {
					Bridge.Client.Store(v.Id, bridge.NewClient(v.Id, nil, nil, nil, version.VERSION))
					logs.Debug("Inserted virtual client for ID %d", v.Id)
				}
			} else {
				v.IsConnect = false
			}
		} else {
			v.IsConnect = false
		}
		v.InletFlow = 0
		v.ExportFlow = 0
		return true
	})
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		h := value.(*file.Host)
		c, err := file.GetDb().GetClient(h.Client.Id)
		if err != nil {
			return true
		}
		c.InletFlow += h.Flow.InletFlow
		c.ExportFlow += h.Flow.ExportFlow
		return true
	})
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		t := value.(*file.Tunnel)
		c, err := file.GetDb().GetClient(t.Client.Id)
		if err != nil {
			return true
		}
		c.InletFlow += t.Flow.InletFlow
		c.ExportFlow += t.Flow.ExportFlow
		return true
	})
	return
}

// DelTunnelAndHostByClientId delete all host and tasks by client id
func DelTunnelAndHostByClientId(clientId int, justDelNoStore bool) {
	var ids []int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*file.Tunnel)
		if justDelNoStore && !v.NoStore {
			return true
		}
		if v.Client.Id == clientId {
			ids = append(ids, v.Id)
		}
		return true
	})
	for _, id := range ids {
		_ = DelTask(id)
	}
	ids = ids[:0]
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*file.Host)
		if justDelNoStore && !v.NoStore {
			return true
		}
		if v.Client.Id == clientId {
			ids = append(ids, v.Id)
		}
		return true
	})
	for _, id := range ids {
		HttpProxyCache.Remove(id)
		_ = file.GetDb().DelHost(id)
	}
}

// DelClientConnect close the client
func DelClientConnect(clientId int) {
	Bridge.DelClient(clientId)
}

var (
	// Cache
	cacheMu         sync.RWMutex
	dashboardCache  map[string]interface{}
	lastRefresh     time.Time
	lastFullRefresh time.Time

	// Net IO
	samplerOnce    sync.Once
	lastBytesSent  uint64
	lastBytesRecv  uint64
	lastSampleTime time.Time
	ioSendRate     atomic.Value // float64
	ioRecvRate     atomic.Value // float64
)

func startSpeedSampler() {
	samplerOnce.Do(func() {
		if io1, _ := net.IOCounters(false); len(io1) > 0 {
			lastBytesSent = io1[0].BytesSent
			lastBytesRecv = io1[0].BytesRecv
		}
		lastSampleTime = time.Now()

		go func() {
			ticker := time.NewTicker(time.Second)
			for now := range ticker.C {
				if io2, _ := net.IOCounters(false); len(io2) > 0 {
					sent := io2[0].BytesSent
					recv := io2[0].BytesRecv
					elapsed := now.Sub(lastSampleTime).Seconds()

					// calculate bytes/sec
					rateSent := float64(sent-lastBytesSent) / elapsed
					rateRecv := float64(recv-lastBytesRecv) / elapsed

					ioSendRate.Store(rateSent)
					ioRecvRate.Store(rateRecv)

					lastBytesSent = sent
					lastBytesRecv = recv
					lastSampleTime = now
				}
			}
		}()
	})
}

func InitDashboardData() {
	startSpeedSampler()
	GetDashboardData(true)
	return
}

func GetDashboardData(force bool) map[string]interface{} {
	cacheMu.RLock()
	cached := dashboardCache
	lastR := lastRefresh
	lastFR := lastFullRefresh
	cacheMu.RUnlock()
	if cached != nil && !force && time.Since(lastFR) < 5*time.Second {
		if time.Since(lastR) < 1*time.Second {
			return cached
		}
		cached["upTime"] = common.GetRunTime()
		tcpCount := 0
		file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
			tcpCount += int(value.(*file.Client).NowConn)
			return true
		})
		cached["tcpCount"] = tcpCount
		cpuPercet, _ := cpu.Percent(0, true)
		var cpuAll float64
		for _, v := range cpuPercet {
			cpuAll += v
		}
		loads, _ := load.Avg()
		cached["load"] = loads.String()
		cached["cpu"] = math.Round(cpuAll / float64(len(cpuPercet)))
		swap, _ := mem.SwapMemory()
		cached["swap_mem"] = math.Round(swap.UsedPercent)
		vir, _ := mem.VirtualMemory()
		cached["virtual_mem"] = math.Round(vir.UsedPercent)
		conn, _ := net.ProtoCounters(nil)
		if v, ok := ioSendRate.Load().(float64); ok {
			cached["io_send"] = v
		}
		if v, ok := ioRecvRate.Load().(float64); ok {
			cached["io_recv"] = v
		}
		for _, v := range conn {
			cached[v.Protocol] = v.Stats["CurrEstab"]
		}
		cacheMu.RLock()
		lastRefresh = time.Now()
		cacheMu.RUnlock()
		return cached
	}
	data := make(map[string]interface{})
	data["version"] = version.VERSION
	data["minVersion"] = GetMinVersion()
	data["hostCount"] = common.GetSyncMapLen(&file.GetDb().JsonDb.Hosts)
	data["clientCount"] = common.GetSyncMapLen(&file.GetDb().JsonDb.Clients)
	if beego.AppConfig.String("public_vkey") != "" { //remove public vkey
		data["clientCount"] = data["clientCount"].(int) - 1
	}
	dealClientData()
	c := 0
	var in, out int64
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if v.IsConnect {
			c += 1
		}
		clientIn := v.Flow.InletFlow - (v.InletFlow + v.ExportFlow)
		if clientIn < 0 {
			clientIn = 0
		}
		clientOut := v.Flow.ExportFlow - (v.InletFlow + v.ExportFlow)
		if clientOut < 0 {
			clientOut = 0
		}
		in += v.InletFlow + clientIn/2
		out += v.ExportFlow + clientOut/2
		return true
	})
	data["clientOnlineCount"] = c
	data["inletFlowCount"] = int(in)
	data["exportFlowCount"] = int(out)
	var tcp, udp, secret, socks5, p2p, http int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		switch value.(*file.Tunnel).Mode {
		case "tcp":
			tcp += 1
		case "socks5":
			socks5 += 1
		case "httpProxy":
			http += 1
		case "mixProxy":
			if value.(*file.Tunnel).HttpProxy {
				http += 1
			}
			if value.(*file.Tunnel).Socks5Proxy {
				socks5 += 1
			}
		case "udp":
			udp += 1
		case "p2p":
			p2p += 1
		case "secret":
			secret += 1
		}
		return true
	})
	data["tcpC"] = tcp
	data["udpCount"] = udp
	data["socks5Count"] = socks5
	data["httpProxyCount"] = http
	data["secretCount"] = secret
	data["p2pCount"] = p2p
	bridgeType := beego.AppConfig.String("bridge_type")
	if bridgeType == "both" {
		bridgeType = "tcp"
	}
	data["bridgeType"] = bridgeType
	data["httpProxyPort"] = beego.AppConfig.String("http_proxy_port")
	data["httpsProxyPort"] = beego.AppConfig.String("https_proxy_port")
	data["ipLimit"] = beego.AppConfig.String("ip_limit")
	data["flowStoreInterval"] = beego.AppConfig.String("flow_store_interval")
	data["serverIp"] = common.GetServerIp()
	data["serverIpv4"] = common.GetOutboundIP().String()
	data["serverIpv6"] = common.GetOutboundIPv6().String()
	data["p2pIp"] = beego.AppConfig.String("p2p_ip")
	data["p2pPort"] = beego.AppConfig.String("p2p_port")
	data["p2pAddr"] = common.JoinHostPort(common.GetServerIp(), beego.AppConfig.String("p2p_port"))
	data["logLevel"] = beego.AppConfig.String("log_level")
	data["upTime"] = common.GetRunTime()
	data["upSecs"] = common.GetRunSecs()
	data["startTime"] = common.GetStartTime()
	tcpCount := 0
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		tcpCount += int(value.(*file.Client).NowConn)
		return true
	})
	data["tcpCount"] = tcpCount
	cpuPercet, _ := cpu.Percent(0, true)
	var cpuAll float64
	for _, v := range cpuPercet {
		cpuAll += v
	}
	loads, _ := load.Avg()
	data["load"] = loads.String()
	data["cpu"] = math.Round(cpuAll / float64(len(cpuPercet)))
	swap, _ := mem.SwapMemory()
	data["swap_mem"] = math.Round(swap.UsedPercent)
	vir, _ := mem.VirtualMemory()
	data["virtual_mem"] = math.Round(vir.UsedPercent)
	conn, _ := net.ProtoCounters(nil)
	if v, ok := ioSendRate.Load().(float64); ok {
		data["io_send"] = v
	}
	if v, ok := ioRecvRate.Load().(float64); ok {
		data["io_recv"] = v
	}
	for _, v := range conn {
		data[v.Protocol] = v.Stats["CurrEstab"]
	}
	//chart
	var fg int
	if len(tool.ServerStatus) >= 10 {
		fg = len(tool.ServerStatus) / 10
		for i := 0; i <= 9; i++ {
			data["sys"+strconv.Itoa(i+1)] = tool.ServerStatus[i*fg]
		}
	}
	cacheMu.RLock()
	dashboardCache = data
	lastRefresh = time.Now()
	lastFullRefresh = time.Now()
	cacheMu.RUnlock()
	return data
}

func GetVersion() string {
	return version.VERSION
}

func GetMinVersion() string {
	return version.GetMinVersion(bridge.ServerSecureMode)
}

func GetCurrentYear() int {
	return time.Now().Year()
}

func flowSession(m time.Duration) {
	file.GetDb().JsonDb.StoreHostToJsonFile()
	file.GetDb().JsonDb.StoreTasksToJsonFile()
	file.GetDb().JsonDb.StoreClientsToJsonFile()
	file.GetDb().JsonDb.StoreGlobalToJsonFile()
	once.Do(func() {
		go func() {
			ticker := time.NewTicker(m)
			defer ticker.Stop()
			for range ticker.C {
				file.GetDb().JsonDb.StoreHostToJsonFile()
				file.GetDb().JsonDb.StoreTasksToJsonFile()
				file.GetDb().JsonDb.StoreClientsToJsonFile()
				file.GetDb().JsonDb.StoreGlobalToJsonFile()
			}
		}()
	})
}
