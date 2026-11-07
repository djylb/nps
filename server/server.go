package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/internal/lifecycle"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/httpproxy"
	"github.com/djylb/nps/server/ports"
)

const pingTimeout = 15 * time.Second

// ErrClosed is returned by the operations of a server that is closed.
var ErrClosed = errors.New("server closed")

// errTaskNotRunning is the error of stopping a task that does not run; the
// web manager recognizes its text.
var errTaskNotRunning = errors.New("task is not running")

// Config holds what a Server needs beyond the store and the current
// configuration.
type Config struct {
	// DisconnectTimeout is how long, in seconds, a silent client tunnel lives.
	DisconnectTimeout int
	// Transports are the bridge transports that listen on a port of their own.
	Transports bridge.Transports
}

// Server is a running nps: the bridge the clients connect to, the tasks it
// serves, the web manager and the background work around them. Start starts
// it, Close stops it and waits for everything it started.
type Server struct {
	db         *file.DbUtils
	bridge     *bridge.Bridge
	tasks      sync.Map // task id -> proxy.Service, or nil for a task without a listener
	hostCache  *index.AnyIntIndex
	transports bridge.Transports

	webHandler   atomic.Pointer[webHandlerBox]
	ipRegHandler http.Handler
	web          *WebServer
	webhook      webhookState
	dash         dashboard

	group      lifecycle.Group // the goroutines Close waits for
	ctx        context.Context // done once the server stops
	cancel     context.CancelFunc
	started    atomic.Bool
	stopParent func() bool
	failed     chan error // the first failure after Start
	closersMu  sync.Mutex
	closers    []io.Closer // the listeners of the registration page
	stopOnce   sync.Once
}

// New returns a server of the entries in db; nothing runs before Start.
func New(db *file.DbUtils, cfg Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		db:         db,
		hostCache:  index.NewAnyIntIndex(),
		transports: cfg.Transports,
		ctx:        ctx,
		cancel:     cancel,
		failed:     make(chan error, 1),
	}
	s.bridge = bridge.NewTunnel(&s.tasks, cfg.DisconnectTimeout)
	s.bridge.SetTunnelLookup(s.lookupTunnel)
	return s
}

// Bridge returns the bridge of the server.
func (s *Server) Bridge() *bridge.Bridge { return s.bridge }

// lookupTunnel finds the running tunnel that tunnel:// targets of id reach.
func (s *Server) lookupTunnel(id int) (bridge.TunnelDialer, bool) {
	if v, ok := s.tasks.Load(id); ok {
		if svr, ok := v.(*proxy.TunnelModeServer); ok {
			if !strings.Contains(svr.Task.Target.TargetStr, "tunnel://") {
				return svr, true
			}
		}
	}
	return nil, false
}

// Start opens the bridge listeners, starts the tasks of the store, the web
// manager and the background work. It returns the first error that keeps
// nps from starting; Close then releases what was started. Failures after
// that are reported through Failed. The server stops when ctx is done, and
// Close waits for that.
func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("server already started")
	}
	if !s.group.Go(s.stopWhenDone) {
		return ErrClosed
	}
	s.stopParent = context.AfterFunc(ctx, s.cancel)
	cfg := servercfg.Current()
	proxy.SetGlobalSource(func() *acl.Source {
		if g := s.db.GetGlobal(); g != nil {
			return g.SourceACL()
		}
		return nil
	})
	s.startIPRegister(cfg)
	startP2P(s.bridge, cfg)
	if err := s.bridge.StartTunnel(s.transports, s.reportFailure); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}
	s.group.Go(s.dealBridgeTask)
	s.ApplyWebhook(cfg.Webhook)
	s.group.Go(s.dealClientFlow)
	if minute := cfg.Runtime.FlowStoreInterval; minute > 0 {
		s.group.Go(func() { s.flowSession(time.Minute * time.Duration(minute)) })
	}
	s.initDashboardData()
	s.initFromDb()
	_ = s.AddTunnel(&model.Tunnel{Port: 0, Mode: "httpHostServer", Status: true})
	s.web = NewWebServer(s)
	web := s.web
	s.group.Go(func() {
		if err := web.Start(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.reportFailure(fmt.Errorf("webServer: %w", err))
		}
	})
	return nil
}

// Failed reports the first listener that stops on an error after Start has
// returned; nps cannot serve as configured after that.
func (s *Server) Failed() <-chan error { return s.failed }

func (s *Server) reportFailure(err error) {
	if s.ctx.Err() != nil {
		return
	}
	select {
	case s.failed <- err:
	default:
		logs.Error("%v", err)
	}
}

// Close stops the server and returns once every goroutine it started has
// returned. It first stops accepting management and registration requests,
// then closes the tasks, then the bridge with its clients, then the webhook.
func (s *Server) Close() error {
	s.cancel()
	s.stopOnce.Do(s.stop)
	s.group.Wait()
	return nil
}

// stopWhenDone stops the server once its context is done.
func (s *Server) stopWhenDone() {
	<-s.ctx.Done()
	s.stopOnce.Do(s.stop)
}

func (s *Server) stop() {
	s.group.Close()
	if s.stopParent != nil {
		s.stopParent()
	}
	s.closersMu.Lock()
	closers := s.closers
	s.closers = nil
	s.closersMu.Unlock()
	for _, c := range closers {
		_ = c.Close()
	}
	if s.web != nil {
		_ = s.web.Close()
	}
	s.tasks.Range(func(key, value any) bool {
		s.tasks.CompareAndDelete(key, value)
		if svr, ok := value.(proxy.Service); ok {
			_ = svr.Close()
		}
		return true
	})
	_ = s.bridge.Close()
	s.ApplyWebhook(servercfg.WebhookConfig{})
}

// addCloser has stop close c, or closes c when the server is stopping.
func (s *Server) addCloser(c io.Closer) bool {
	s.closersMu.Lock()
	defer s.closersMu.Unlock()
	if s.ctx.Err() != nil {
		_ = c.Close()
		return false
	}
	s.closers = append(s.closers, c)
	return true
}

// initFromDb starts the enabled tasks of the store.
func (s *Server) initFromDb() {
	cfg := servercfg.Current()
	if cfg.Feature.AllowLocalProxy {
		if _, err := s.db.GetClient(-1); err != nil {
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
			s.db.JsonDb.Clients.Store(local.Id, local)
			logs.Info("Auto create local proxy client.")
		}
	}

	s.addKeyClients(cfg.Runtime.PublicVKey, cfg.Runtime.VisitorVKey)
	//Initialize services in server-side files
	s.db.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if value.(*model.Tunnel).Status {
			_ = s.AddTunnel(value.(*model.Tunnel))
		}
		return true
	})
}

// addKeyClients creates the unstored clients of the public and visitor keys.
func (s *Server) addKeyClients(public, visitor string) {
	if public != "" {
		s.addKeyClient(public, model.KeyPublic)
	}
	// the public key can already do all that the visitor key can
	if visitor != "" && visitor != public {
		s.addKeyClient(visitor, model.KeyVisitor)
	}
}

func (s *Server) addKeyClient(vkey string, kind model.KeyKind) {
	c := model.NewClient(vkey, true, true)
	c.KeyKind = kind
	if err := s.db.NewClient(c); err != nil {
		logs.Warn("public_vkey or visitor_vkey ignored, use a key of its own: %v", err)
		return
	}
	s.tasks.Store(c.Id, nil)
}

// routePruneInterval is how often departed nodes lose their routes.
const routePruneInterval = 2 * time.Second

// dealBridgeTask handles the requests of the bridge until the server stops.
func (s *Server) dealBridgeTask() {
	prune := time.NewTicker(routePruneInterval)
	defer prune.Stop()
	b := s.bridge
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-prune.C:
			b.PruneRoutes(func(id int) { _ = s.DeleteTunnel(id) }, s.delHost)
		case h := <-b.OpenHost:
			if h != nil {
				s.hostCache.Remove(h.Id)
			}
		case t := <-b.OpenTask:
			if t != nil {
				_ = s.StopTunnel(t.Id)
				if err := s.StartTunnel(t.Id); err != nil {
					logs.Error("StartTask(%d) error: %v", t.Id, err)
				}
			}
		case t := <-b.CloseTask:
			if t != nil {
				_ = s.StopTunnel(t.Id)
			}
		case id := <-b.CloseClient:
			b.CloseIfDisconnected(id, func() {
				s.deleteClientEntries(id, true)
				if v, ok := s.db.JsonDb.Clients.Load(id); ok {
					if v.(*model.Client).NoStore {
						_ = s.db.DelClient(id)
					}
				}
			})
		case sc := <-b.SecretChan:
			if sc != nil {
				s.handleSecret(sc)
			}
		}
	}
}

func (s *Server) handleSecret(sc *conn.Secret) {
	logs.Trace("New secret connection, addr %v", sc.Conn.Conn.RemoteAddr())
	t := s.db.GetTaskByMd5Password(sc.Password)
	if t == nil {
		logs.Trace("This key %s cannot be processed", sc.Password)
		_ = sc.Conn.Close()
		return
	}
	if !t.Status {
		_ = sc.Conn.Close()
		logs.Trace("This key %s cannot be processed,status is close", sc.Password)
		return
	}
	feature := servercfg.Current().Feature
	if !s.group.Go(func() {
		_ = proxy.NewSecretServer(s.bridge, t, feature.AllowLocalProxy, feature.AllowSecretLink, feature.AllowSecretLocal).HandleSecret(sc.Conn, sc.Wire)
	}) {
		_ = sc.Conn.Close()
	}
}

func (s *Server) dealClientFlow() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.dealClientData()
		case <-s.ctx.Done():
			return
		}
	}
}

// PingClient measures the round trip to client id, -1 when it fails.
func (s *Server) PingClient(id int, addr string) int {
	if id <= 0 {
		return 0
	}
	link := conn.NewLink(conn.LinkPing, "", false, false, addr, false)
	link.Option.NeedAck = true
	link.Option.Timeout = pingTimeout
	start := time.Now()
	target, err := s.bridge.Session(id).OpenLink(link, 0)
	if err != nil {
		logs.Warn("get connection from client Id %d error %v", id, err)
		return -1
	}
	rtt := int(time.Since(start).Milliseconds())
	_ = target.Close()
	return rtt
}

// newMode returns the service of task c's mode, nil for an unknown mode.
func (s *Server) newMode(c *model.Tunnel) proxy.Service {
	var service proxy.Service
	cfg := servercfg.Current()
	allowLocalProxy := cfg.Feature.AllowLocalProxy
	b := s.bridge
	switch c.Mode {
	case "tcp", "file":
		service = proxy.NewTunnelModeServer(proxy.ProcessTunnel, b, c, allowLocalProxy)
	case "mixProxy", "socks5", "httpProxy":
		service = proxy.NewTunnelModeServer(proxy.ProcessMix, b, c, allowLocalProxy)
	case "tcpTrans":
		service = proxy.NewTunnelModeServer(proxy.HandleTrans, b, c, allowLocalProxy)
	case "udp":
		service = proxy.NewUdpModeServer(b, c, allowLocalProxy)
	case "httpHostServer":
		httpPort := connection.Network().HTTPProxyPort
		httpsPort := connection.Network().HTTPSProxyPort
		http3Port := connection.Network().HTTP3ProxyPort
		// http cache settings are currently unused.
		addOrigin := cfg.Proxy.AddOriginHeader
		httpOnlyPass := cfg.Auth.HTTPOnlyPass
		service = httpproxy.NewHttpProxy(b, c, httpPort, httpsPort, http3Port, httpOnlyPass, addOrigin, allowLocalProxy, s.hostCache)
	}
	return service
}

// StopTunnel stops task id and marks it stopped in the store.
func (s *Server) StopTunnel(id int) error {
	if t, err := s.db.GetTask(id); err != nil {
		return err
	} else {
		t.Status = false
		logs.Info("close port %d,remark %s,client id %d,task id %d", t.Port, t.Remark, t.Client.Id, t.Id)
		_ = s.db.UpdateTask(t)
	}
	v, ok := s.tasks.Load(id)
	if !ok {
		return errTaskNotRunning
	}
	if svr, ok := v.(proxy.Service); ok {
		if err := svr.Close(); err != nil {
			logs.Warn("stop server id %d error %v", id, err)
		} else {
			logs.Info("stop server id %d", id)
		}
	}
	s.tasks.CompareAndDelete(id, v)
	return nil
}

// AddTunnel starts task t. Services that own a listening socket bind it
// before AddTunnel returns, so address errors are reported to the caller and
// a later StopTunnel always finds the running instance.
func (s *Server) AddTunnel(t *model.Tunnel) error {
	if s.group.Closed() {
		return ErrClosed
	}
	if t.Mode == "secret" || t.Mode == "p2p" {
		logs.Info("secret task %s start ", t.Remark)
		s.tasks.Store(t.Id, nil)
		return nil
	}
	if b := ports.TestTunnelPort(t); !b && t.Mode != "httpHostServer" {
		logs.Error("taskId %d start error port %d open failed", t.Id, t.Port)
		return errors.New("the port open error")
	}
	svr := s.newMode(t)
	if svr == nil {
		return errors.New("the mode is not correct")
	}
	if b, ok := svr.(proxy.Binder); ok {
		if err := b.Bind(); err != nil {
			logs.Error("taskId %d start error port %d open failed: %v", t.Id, t.Port, err)
			return err
		}
	}
	if old, loaded := s.tasks.Swap(t.Id, svr); loaded {
		if oldSvr, ok := old.(proxy.Service); ok {
			_ = oldSvr.Close()
		}
	}
	logs.Info("tunnel task %s start mode：%s port %d", t.Remark, t.Mode, t.Port)
	// The labels name the task in stack dumps and tracebacks of everything
	// the server starts.
	labels := pprof.Labels("task", strconv.Itoa(t.Id), "mode", t.Mode, "client", strconv.Itoa(clientID(t)))
	if !s.group.Go(func() {
		pprof.Do(context.Background(), labels, func(context.Context) {
			if err := svr.Start(); err != nil && !errors.Is(err, proxy.ErrServerClosed) {
				logs.Error("taskId %d start error %v", t.Id, err)
				s.tasks.CompareAndDelete(t.Id, svr)
				_ = svr.Close()
			}
		})
	}) {
		s.tasks.CompareAndDelete(t.Id, svr)
		_ = svr.Close()
		return ErrClosed
	}
	return nil
}

func clientID(t *model.Tunnel) int {
	if t.Client == nil {
		return 0
	}
	return t.Client.Id
}

// TunnelRunning reports whether task id runs.
func (s *Server) TunnelRunning(id int) bool {
	_, ok := s.tasks.Load(id)
	return ok
}

// StartTunnel starts task id of the store and marks it started.
func (s *Server) StartTunnel(id int) error {
	t, err := s.db.GetTask(id)
	if err != nil {
		return err
	}
	if err = s.AddTunnel(t); err != nil {
		return err
	}
	t.Status = true
	_ = s.db.UpdateTask(t)
	return nil
}

// DeleteTunnel stops task id and removes it from the store.
func (s *Server) DeleteTunnel(id int) error {
	if _, ok := s.tasks.Load(id); ok {
		_ = s.StopTunnel(id)
	}
	return s.db.DelTask(id)
}

// DeleteClientResources removes all tasks and domain entries of client id.
func (s *Server) DeleteClientResources(id int) { s.deleteClientEntries(id, false) }

// deleteClientEntries removes the tasks and domain entries of client
// clientId, only the unstored ones if justDelNoStore.
func (s *Server) deleteClientEntries(clientId int, justDelNoStore bool) {
	var ids []int
	s.db.JsonDb.Tasks.Range(func(key, value interface{}) bool {
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
		_ = s.DeleteTunnel(id)
	}
	ids = ids[:0]
	s.db.JsonDb.Hosts.Range(func(key, value interface{}) bool {
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
		s.delHost(id)
	}
}

func (s *Server) delHost(id int) {
	s.hostCache.Remove(id)
	_ = s.db.DelHost(id)
}

// RemoveHostCache drops what the HTTP proxy cached of domain entry id.
func (s *Server) RemoveHostCache(id int) { s.hostCache.Remove(id) }

// RequestClientUpdate asks the connected npc of client clientId to update
// themselves; it returns how many were asked.
func (s *Server) RequestClientUpdate(clientId int) (int, error) {
	return s.bridge.RequestClientUpdate(clientId)
}

// DisconnectClient closes the connections of client clientId.
func (s *Server) DisconnectClient(clientId int) { s.bridge.DelClient(clientId) }

// ClientConnected reads live bridge membership without refreshing or
// sorting lists.
func (s *Server) ClientConnected(id int) bool { return s.bridge.Connected(id) }

// GenerateTunnelPort picks a free port for t.
func (s *Server) GenerateTunnelPort(t *model.Tunnel) int { return ports.GenerateTunnelPort(t) }

// TunnelPortAvailable reports whether t may listen on its port.
func (s *Server) TunnelPortAvailable(t *model.Tunnel) bool { return ports.TestTunnelPort(t) }

func (s *Server) dealClientData() {
	b := s.bridge
	s.db.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if vv, ok := b.Client.Load(v.Id); ok {
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
				v.LocalAddr = netaddr.GetOutboundIP().String()
				// Add Local Client
				if !b.Connected(v.Id) && v.Status {
					b.Client.Store(v.Id, bridge.NewClient(v.Id, bridge.NewLocalNode("127.0.0.1", version.VERSION)))
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
		c, err := s.db.GetClient(owner.Id)
		if err != nil {
			return
		}
		v := f.Values()
		sum := sums[c]
		sum.in += v.InletFlow
		sum.out += v.ExportFlow
		sums[c] = sum
	}
	s.db.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		if h, ok := value.(*model.Host); ok && h != nil {
			addFlow(h.Client, h.Flow)
		}
		return true
	})
	s.db.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if t, ok := value.(*model.Tunnel); ok && t != nil {
			addFlow(t.Client, t.Flow)
		}
		return true
	})
	s.db.JsonDb.Clients.Range(func(key, value interface{}) bool {
		c := value.(*model.Client)
		sum := sums[c]
		c.InletFlow.Store(sum.in)
		c.ExportFlow.Store(sum.out)
		return true
	})
}

// flowSession periodically persists flow counters and other runtime state.
func (s *Server) flowSession(m time.Duration) {
	ticker := time.NewTicker(m)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.storeAll()
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) storeAll() {
	db := s.db.JsonDb
	for _, store := range []func() error{db.StoreHostToJsonFile, db.StoreTasksToJsonFile, db.StoreClientsToJsonFile, db.StoreGlobalToJsonFile} {
		if err := store(); err != nil {
			logs.Error("%v", err)
		}
	}
}
