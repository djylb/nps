package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/httpproxy"
	"github.com/djylb/nps/server/ports"
)

var (
	Bridge         *bridge.Bridge
	RunList        sync.Map //map[int]interface{}
	HttpProxyCache = index.NewAnyIntIndex()
)

const pingTimeout = 15 * time.Second

func init() {
	RunList = sync.Map{}
	bridge.SetTunnelLookup(func(id int) (bridge.TunnelDialer, bool) {
		if v, ok := RunList.Load(id); ok {
			if svr, ok := v.(*proxy.TunnelModeServer); ok {
				if !strings.Contains(svr.Task.Target.TargetStr, "tunnel://") {
					return svr, true
				}
			}
		}
		return nil, false
	})
	proxy.SetGlobalSource(func() *acl.Source {
		if g := file.GetDb().GetGlobal(); g != nil {
			return g.SourceACL()
		}
		return nil
	})
}

// InitFromDb init task from db
func InitFromDb() {
	cfg := servercfg.Current()
	if cfg.Feature.AllowLocalProxy {
		db := file.GetDb()
		if _, err := db.GetClient(-1); err != nil {
			local := new(model.Client)
			local.Id = -1
			local.Remark = "Local Proxy"
			local.Addr = "127.0.0.1"
			local.Cnf = new(model.Config)
			local.Flow = new(model.Flow)
			local.Flow.MarkClient()
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

	addKeyClients(cfg.Runtime.PublicVKey, cfg.Runtime.VisitorVKey)
	//Initialize services in server-side files
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if value.(*model.Tunnel).Status {
			_ = AddTask(value.(*model.Tunnel))
		}
		return true
	})
}

// addKeyClients creates the unstored clients of the public and visitor keys.
func addKeyClients(public, visitor string) {
	if public != "" {
		addKeyClient(public, model.KeyPublic)
	}
	// the public key can already do all that the visitor key can
	if visitor != "" && visitor != public {
		addKeyClient(visitor, model.KeyVisitor)
	}
}

func addKeyClient(vkey string, kind model.KeyKind) {
	c := model.NewClient(vkey, true, true)
	c.KeyKind = kind
	if err := file.GetDb().NewClient(c); err != nil {
		logs.Warn("public_vkey or visitor_vkey ignored, use a key of its own: %v", err)
		return
	}
	RunList.Store(c.Id, nil)
}

// routePruneInterval is how often departed nodes lose their routes.
const routePruneInterval = 2 * time.Second

// DealBridgeTask get bridge command
func DealBridgeTask() {
	prune := time.NewTicker(routePruneInterval)
	defer prune.Stop()
	for {
		select {
		case <-prune.C:
			Bridge.PruneRoutes(func(id int) { _ = DelTask(id) }, delHost)
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
			Bridge.CloseIfDisconnected(id, func() {
				DelTunnelAndHostByClientId(id, true)
				if v, ok := file.GetDb().JsonDb.Clients.Load(id); ok {
					if v.(*model.Client).NoStore {
						_ = file.GetDb().DelClient(id)
					}
				}
			})
		//case tunnel := <-Bridge.OpenTask:
		//	_ = StartTask(tunnel.Id)
		case s := <-Bridge.SecretChan:
			if s != nil {
				logs.Trace("New secret connection, addr %v", s.Conn.Conn.RemoteAddr())
				if t := file.GetDb().GetTaskByMd5Password(s.Password); t != nil {
					if t.Status {
						feature := servercfg.Current().Feature
						go func() {
							_ = proxy.NewSecretServer(Bridge, t, feature.AllowLocalProxy, feature.AllowSecretLink, feature.AllowSecretLocal).HandleSecret(s.Conn, s.Wire)
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
func StartNewServer(cnf *model.Tunnel, bridgeDisconnect int) {
	cfg := servercfg.Current()
	Bridge = bridge.NewTunnel(&RunList, bridgeDisconnect)
	startP2P(Bridge, cfg)
	go func() {
		if err := Bridge.StartTunnel(); err != nil {
			logs.Error("start server bridge error %v", err)
			os.Exit(1)
		}
	}()
	go DealBridgeTask()
	ApplyWebhook(cfg.Webhook)
	go dealClientFlow()
	if minute := cfg.Runtime.FlowStoreInterval; minute > 0 {
		go flowSession(time.Minute * time.Duration(minute))
	}
	InitDashboardData()
	// RunList owns the server; stopping the task closes it.
	//noinspection GoResourceLeak
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

func PingClient(id int, addr string) int {
	if id <= 0 {
		return 0
	}
	link := conn.NewLink("ping", "", false, false, addr, false)
	link.Option.NeedAck = true
	link.Option.Timeout = pingTimeout
	start := time.Now()
	target, err := Bridge.Session(id).OpenLink(link, 0)
	if err != nil {
		logs.Warn("get connection from client Id %d error %v", id, err)
		return -1
	}
	rtt := int(time.Since(start).Milliseconds())
	_ = target.Close()
	return rtt
}

// NewMode new a server by mode name
func NewMode(Bridge *bridge.Bridge, c *model.Tunnel) proxy.Service {
	var service proxy.Service
	cfg := servercfg.Current()
	allowLocalProxy := cfg.Feature.AllowLocalProxy
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
		t := &model.Tunnel{
			Port:   0,
			Mode:   "httpHostServer",
			Status: true,
		}
		_ = AddTask(t)
		service = NewWebServer(Bridge)
	case "httpHostServer":
		httpPort := connection.Network().HTTPProxyPort
		httpsPort := connection.Network().HTTPSProxyPort
		http3Port := connection.Network().HTTP3ProxyPort
		// http cache settings are currently unused.
		addOrigin := cfg.Proxy.AddOriginHeader
		httpOnlyPass := cfg.Auth.HTTPOnlyPass
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
	v, ok := RunList.Load(id)
	if !ok {
		return errors.New("task is not running")
	}
	if svr, ok := v.(proxy.Service); ok {
		if err := svr.Close(); err != nil {
			logs.Warn("stop server id %d error %v", id, err)
		} else {
			logs.Info("stop server id %d", id)
		}
	}
	RunList.CompareAndDelete(id, v)
	return nil
}

// AddTask add task. Services that own a listening socket bind it before
// AddTask returns, so address errors are reported to the caller and a later
// StopServer always finds the running instance in RunList.
func AddTask(t *model.Tunnel) error {
	if t.Mode == "secret" || t.Mode == "p2p" {
		logs.Info("secret task %s start ", t.Remark)
		RunList.Store(t.Id, nil)
		return nil
	}
	if b := ports.TestTunnelPort(t); !b && t.Mode != "httpHostServer" {
		logs.Error("taskId %d start error port %d open failed", t.Id, t.Port)
		return errors.New("the port open error")
	}
	svr := NewMode(Bridge, t)
	if svr == nil {
		return errors.New("the mode is not correct")
	}
	if b, ok := svr.(proxy.Binder); ok {
		if err := b.Bind(); err != nil {
			logs.Error("taskId %d start error port %d open failed: %v", t.Id, t.Port, err)
			return err
		}
	}
	if old, loaded := RunList.Swap(t.Id, svr); loaded {
		if oldSvr, ok := old.(proxy.Service); ok {
			_ = oldSvr.Close()
		}
	}
	logs.Info("tunnel task %s start mode：%s port %d", t.Remark, t.Mode, t.Port)
	// The labels name the task in stack dumps and tracebacks of everything
	// the server starts.
	labels := pprof.Labels("task", strconv.Itoa(t.Id), "mode", t.Mode, "client", strconv.Itoa(clientID(t)))
	go pprof.Do(context.Background(), labels, func(context.Context) {
		if err := svr.Start(); err != nil && !errors.Is(err, proxy.ErrServerClosed) {
			logs.Error("taskId %d start error %v", t.Id, err)
			RunList.CompareAndDelete(t.Id, svr)
			_ = svr.Close()
		}
	})
	return nil
}

func clientID(t *model.Tunnel) int {
	if t.Client == nil {
		return 0
	}
	return t.Client.Id
}

// StartTask start task
func StartTask(id int) error {
	t, err := file.GetDb().GetTask(id)
	if err != nil {
		return err
	}
	if err = AddTask(t); err != nil {
		return err
	}
	t.Status = true
	_ = file.GetDb().UpdateTask(t)
	return nil
}

// DelTask delete task
func DelTask(id int) error {
	if _, ok := RunList.Load(id); ok {
		_ = StopServer(id)
	}
	return file.GetDb().DelTask(id)
}

// DelTunnelAndHostByClientId delete all host and tasks by client id
func DelTunnelAndHostByClientId(clientId int, justDelNoStore bool) {
	var ids []int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*model.Tunnel)
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
		v := value.(*model.Host)
		if justDelNoStore && !v.NoStore {
			return true
		}
		if v.Client.Id == clientId {
			ids = append(ids, v.Id)
		}
		return true
	})
	for _, id := range ids {
		delHost(id)
	}
}

func delHost(id int) {
	HttpProxyCache.Remove(id)
	_ = file.GetDb().DelHost(id)
}

// RequestClientUpdate asks the connected npc of client clientId to update
// themselves; it returns how many were asked.
func RequestClientUpdate(clientId int) (int, error) {
	return Bridge.RequestClientUpdate(clientId)
}

// DelClientConnect close the client
func DelClientConnect(clientId int) {
	Bridge.DelClient(clientId)
}

func dealClientData() {
	//logs.Info("dealClientData.........")
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if vv, ok := Bridge.Client.Load(v.Id); ok {
			v.IsConnect = true
			v.MarkSeen(time.Now())
			cli := vv.(*bridge.Client)
			node, ok := cli.GetNodeByUUID(cli.LastUUID)
			var ver string
			if ok {
				ver = node.Version
			}
			count := cli.NodeCount()
			if count > 1 {
				ver = fmt.Sprintf("%s(%d)", ver, cli.NodeCount())
			}
			v.Version = ver
		} else if v.Id <= 0 {
			if servercfg.Current().Feature.AllowLocalProxy {
				v.IsConnect = v.Status
				v.Version = version.VERSION
				v.Mode = "local"
				v.LocalAddr = common.GetOutboundIP().String()
				// Add Local Client
				if !Bridge.Connected(v.Id) && v.Status {
					Bridge.Client.Store(v.Id, bridge.NewClient(v.Id, bridge.NewLocalNode("127.0.0.1", version.VERSION)))
					logs.Debug("Inserted virtual client for ID %d", v.Id)
				}
			} else {
				v.IsConnect = false
			}
		} else {
			v.IsConnect = false
		}
		return true
	})
	// Sum the traffic of each client's hosts and tasks first, so that the
	// client totals are set once per pass.
	type inOut struct{ in, out int64 }
	sums := make(map[*model.Client]inOut)
	addFlow := func(owner *model.Client, f *model.Flow) {
		if owner == nil {
			return
		}
		c, err := file.GetDb().GetClient(owner.Id)
		if err != nil {
			return
		}
		v := f.Values()
		sum := sums[c]
		sum.in += v.InletFlow
		sum.out += v.ExportFlow
		sums[c] = sum
	}
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		if h, ok := value.(*model.Host); ok && h != nil {
			addFlow(h.Client, h.Flow)
		}
		return true
	})
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if t, ok := value.(*model.Tunnel); ok && t != nil {
			addFlow(t.Client, t.Flow)
		}
		return true
	})
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		c := value.(*model.Client)
		sum := sums[c]
		c.InletFlow.Store(sum.in)
		c.ExportFlow.Store(sum.out)
		return true
	})
}

// flowSession periodically persists flow counters and other runtime state.
func flowSession(m time.Duration) {
	ticker := time.NewTicker(m)
	defer ticker.Stop()
	for range ticker.C {
		storeAll()
	}
}

func storeAll() {
	db := file.GetDb().JsonDb
	for _, store := range []func() error{db.StoreHostToJsonFile, db.StoreTasksToJsonFile, db.StoreClientsToJsonFile, db.StoreGlobalToJsonFile} {
		if err := store(); err != nil {
			logs.Error("%v", err)
		}
	}
}
