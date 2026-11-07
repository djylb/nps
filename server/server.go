package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/lifecycle"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/obs"
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
	// SecureMode refuses the handshakes of npc versions that predate secure
	// mode.
	SecureMode bool
	// SelectMode is how a client with several nodes picks the node of a new
	// link.
	SelectMode bridge.SelectMode
	// DisableRemoteUpdate keeps the web interface from updating the server.
	DisableRemoteUpdate bool
	// AllowedPorts are the ports tunnels may listen on; the zero value
	// allows every port.
	AllowedPorts ports.Allowed
	// Identity is the certificate of the bridge and the encrypted links, and
	// the key of the web login.
	Identity *crypt.Identity
}

// Server is a running nps: the bridge the clients connect to, the tasks it
// serves, the web manager and the background work around them. Start starts
// it, Close stops it and waits for everything it started.
type Server struct {
	db         *file.DbUtils
	bridge     *bridge.Bridge
	tasks      sync.Map // task id -> proxy.Service, or nil for a task without a listener
	hostCache  *httpproxy.Plans
	transports bridge.Transports
	ports      ports.Allowed
	identity   *crypt.Identity
	noUpdate   bool        // remote update is disabled
	updating   atomic.Bool // an update runs

	webHandler   atomic.Pointer[webHandlerBox]
	ipRegHandler http.Handler
	web          *WebServer
	webhook      webhookState
	appliers     configAppliers
	dash         dashboard

	// scope runs the background work of the server and owns what Close
	// stops, closed in reverse order of registration: the web manager and
	// the registration page, then the tasks, then the bridge, then the
	// webhook. The first task that fails closes it.
	scope   *lifecycle.Scope
	started atomic.Bool
}

// New returns a server of the entries in db; nothing runs before Start.
func New(db *file.DbUtils, cfg Config) *Server {
	s := &Server{
		db:         db,
		hostCache:  httpproxy.NewPlans(),
		transports: cfg.Transports,
		ports:      cfg.AllowedPorts,
		identity:   cfg.Identity,
		noUpdate:   cfg.DisableRemoteUpdate,
		// Once the server stops, its work may end with any error.
		scope: lifecycle.New(context.Background(), lifecycle.CancelOnError().Expected(func(error) bool { return true })),
	}
	s.bridge = bridge.NewTunnel(db, &s.tasks, bridge.Options{
		DisconnectTime: cfg.DisconnectTimeout,
		SecureMode:     cfg.SecureMode,
		SelectMode:     cfg.SelectMode,
		AllowedPorts:   cfg.AllowedPorts,
		Cert:           cfg.Identity.TLSCert(),
	})
	db.OnEntriesChanged(s.bridge.EntriesChanged)
	s.bridge.SetTunnelLookup(s.lookupTunnel)
	// registered first, so closed last
	_, _ = s.scope.Add(closerFunc(func() { s.ApplyWebhook(servercfg.WebhookConfig{}) }))
	s.own(s.bridge)
	_, _ = s.scope.Add(closerFunc(s.closeTasks))
	return s
}

// Store returns the store of the entries the server serves.
func (s *Server) Store() *file.DbUtils { return s.db }

// Identity returns the certificate and login key of the server.
func (s *Server) Identity() *crypt.Identity { return s.identity }

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
// nps from starting; Close then releases what was started. A failure after
// that, of the web manager or of a bridge listener, stops the server: Done
// is closed and Close returns the failure. The server also stops when ctx
// is done.
func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("server already started")
	}
	stop := context.AfterFunc(ctx, func() { s.scope.Close(context.Cause(ctx)) })
	if _, err := s.scope.Add(closerFunc(func() { stop() })); err != nil {
		return ErrClosed
	}
	cfg := servercfg.Current()
	s.startIPRegister(cfg)
	startP2P(s.bridge, cfg)
	if err := s.bridge.StartTunnel(s.transports); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}
	b := s.bridge
	s.goTask("bridge", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-b.Done():
			return b.Cause()
		}
	})
	s.goWork("bridge requests", s.dealBridgeTask)
	s.ApplyWebhook(cfg.Webhook)
	s.goWork("client flow", s.dealClientFlow)
	if minute := cfg.Runtime.FlowStoreInterval; minute > 0 {
		s.goWork("flow store", func(ctx context.Context) { s.flowSession(ctx, time.Minute*time.Duration(minute)) })
	}
	s.initDashboardData()
	s.initFromDb()
	_ = s.AddTunnel(&model.Tunnel{Port: 0, Mode: "httpHostServer", Status: true})
	web := NewWebServer(s)
	if !s.own(web) {
		return ErrClosed
	}
	s.web = web
	s.goTask("web manager", func(context.Context) error { return web.Start() })
	return nil
}

// Done is closed when the server starts stopping: by Close, by the end of
// the context given to Start, or by a failure after Start.
func (s *Server) Done() <-chan struct{} { return s.scope.Done() }

// Close stops the server and returns once every goroutine it started has
// returned. It first stops accepting management and registration requests,
// then closes the tasks, then the bridge with its clients, then the webhook.
// It returns the failure that stopped the server, if one did.
func (s *Server) Close() error {
	s.scope.Close(nil)
	return s.scope.Wait(context.Background())
}

// goTask runs fn until the server stops; an error of fn stops the server.
// Once the server is stopping it does not run fn.
func (s *Server) goTask(name string, fn func(ctx context.Context) error) bool {
	return s.scope.Go(name, fn) == nil
}

// goWork runs fn, which reports no failure, like goTask.
func (s *Server) goWork(name string, fn func(ctx context.Context)) bool {
	return s.goTask(name, func(ctx context.Context) error { fn(ctx); return nil })
}

// closeTasks stops every running task.
func (s *Server) closeTasks() {
	s.tasks.Range(func(key, value any) bool {
		s.tasks.CompareAndDelete(key, value)
		if svr, ok := value.(proxy.Service); ok {
			_ = svr.Close()
		}
		return true
	})
}

// own has Close close c, after what is owned later and before what is owned
// earlier; its error is not a failure of the server. Once the server is
// stopping, own closes c and reports false.
func (s *Server) own(c io.Closer) bool {
	_, err := s.scope.Add(closerFunc(func() { _ = c.Close() }))
	return err == nil
}

// closerFunc is an io.Closer that runs f.
type closerFunc func()

func (f closerFunc) Close() error {
	f()
	return nil
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
			log.Info("local proxy client created")
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
		log.Warn("public_vkey or visitor_vkey ignored, use a key of its own", "err", err)
		return
	}
	s.tasks.Store(c.Id, nil)
}

// routePruneInterval is how often departed nodes lose their routes.
const routePruneInterval = 2 * time.Second

// dealBridgeTask handles the requests of the bridge until the server stops.
func (s *Server) dealBridgeTask(ctx context.Context) {
	prune := time.NewTicker(routePruneInterval)
	defer prune.Stop()
	b := s.bridge
	for {
		select {
		case <-ctx.Done():
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
					log.Error("tunnel start failed", "tunnel", t.Id, "err", err)
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
	log.Debug("secret connection", "addr", sc.Conn.Conn.RemoteAddr())
	t := s.db.GetTaskByMd5Password(sc.Password)
	if t == nil {
		log.Debug("secret connection dropped", "reason", "unknown_key")
		_ = sc.Conn.Close()
		return
	}
	if !t.Status {
		_ = sc.Conn.Close()
		log.Debug("secret connection dropped", "tunnel", t.Id, "reason", "tunnel_stopped")
		return
	}
	feature := servercfg.Current().Feature
	if !s.goWork("secret", func(context.Context) {
		_ = proxy.NewSecretServer(s.bridge, t, feature.AllowLocalProxy, feature.AllowSecretLink, feature.AllowSecretLocal).HandleSecret(sc.Conn, sc.Wire)
	}) {
		_ = sc.Conn.Close()
	}
}

func (s *Server) dealClientFlow(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.dealClientData()
		case <-ctx.Done():
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
		log.Warn("ping client failed", "client", id, "err", err)
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
		service = httpproxy.NewHttpProxy(s.db, b, c, httpPort, httpsPort, http3Port, httpOnlyPass, addOrigin, allowLocalProxy, s.hostCache)
	}
	return service
}

// StopTunnel stops task id and marks it stopped in the store.
func (s *Server) StopTunnel(id int) error {
	if t, err := s.db.GetTask(id); err != nil {
		return err
	} else {
		t.Status = false
		log.Info("tunnel closing", "tunnel", t.Id, "client", t.Client.Id, "port", t.Port, "remark", t.Remark)
		_ = s.db.UpdateTask(t)
	}
	v, ok := s.tasks.Load(id)
	if !ok {
		return errTaskNotRunning
	}
	if svr, ok := v.(proxy.Service); ok {
		if err := svr.Close(); err != nil {
			log.Warn("tunnel stop failed", "tunnel", id, "err", err)
		} else {
			log.Info("tunnel stopped", "tunnel", id)
		}
	}
	s.tasks.CompareAndDelete(id, v)
	return nil
}

// AddTunnel starts task t. Services that own a listening socket bind it
// before AddTunnel returns, so address errors are reported to the caller and
// a later StopTunnel always finds the running instance.
func (s *Server) AddTunnel(t *model.Tunnel) error {
	select {
	case <-s.scope.Done():
		return ErrClosed
	default:
	}
	if t.Mode == "secret" || t.Mode == "p2p" {
		log.Info("tunnel started", "tunnel", t.Id, "mode", t.Mode, "remark", t.Remark)
		s.tasks.Store(t.Id, nil)
		return nil
	}
	if b := s.ports.TestTunnelPort(t); !b && t.Mode != "httpHostServer" {
		obs.Emit(log, &logevent.ListenFailed, "tunnel", t.Id, "route", tunnelRoute(t), "addr", t.Port, "reason", "port unavailable")
		return errors.New("the port open error")
	}
	svr := s.newMode(t)
	if svr == nil {
		return errors.New("the mode is not correct")
	}
	if b, ok := svr.(proxy.Binder); ok {
		if err := b.Bind(); err != nil {
			obs.Emit(log, &logevent.ListenFailed, "tunnel", t.Id, "route", tunnelRoute(t), "addr", t.Port, "reason", logevent.NetReason(err), "err", err)
			return err
		}
	}
	if old, loaded := s.tasks.Swap(t.Id, svr); loaded {
		if oldSvr, ok := old.(proxy.Service); ok {
			_ = oldSvr.Close()
		}
	}
	log.Info("tunnel started", "tunnel", t.Id, "mode", t.Mode, "port", t.Port, "remark", t.Remark)
	// The labels name the task in stack dumps and tracebacks of everything
	// the server starts.
	labels := pprof.Labels("task", strconv.Itoa(t.Id), "mode", t.Mode, "client", strconv.Itoa(clientID(t)))
	if !s.goWork("task "+strconv.Itoa(t.Id), func(ctx context.Context) {
		pprof.Do(ctx, labels, func(context.Context) {
			if err := svr.Start(); err != nil && !errors.Is(err, proxy.ErrServerClosed) {
				obs.Emit(log, &logevent.ListenFailed, "tunnel", t.Id, "route", tunnelRoute(t), "addr", t.Port, "reason", logevent.NetReason(err), "err", err)
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
func (s *Server) GenerateTunnelPort(t *model.Tunnel) int { return s.ports.GenerateTunnelPort(t) }

// TunnelPortAvailable reports whether t may listen on its port.
func (s *Server) TunnelPortAvailable(t *model.Tunnel) bool { return s.ports.TestTunnelPort(t) }

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
					b.Client.Store(v.Id, bridge.NewClient(v.Id, bridge.NewLocalNode("127.0.0.1", version.VERSION), bridge.Primary))
					log.Debug("virtual client inserted", "client", v.Id)
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
func (s *Server) flowSession(ctx context.Context, m time.Duration) {
	ticker := time.NewTicker(m)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.storeAll()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) storeAll() {
	db := s.db.JsonDb
	for _, store := range []func() error{db.StoreHostToJsonFile, db.StoreTasksToJsonFile, db.StoreClientsToJsonFile, db.StoreGlobalToJsonFile} {
		if err := store(); err != nil {
			log.Error("store write failed", "err", err)
		}
	}
}

var log = obs.Module("server")

// tunnelRoute names tunnel t in route fields.
func tunnelRoute(t *model.Tunnel) string { return t.LogRoute() }
