package client

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"runtime/pprof"
	"strconv"
	"sync"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/slogx"
)

type Closer interface{ Close() error }

// SecretManager runs the point-to-point access sections of a visitor: their
// listeners and one visitor per secret task (password) and conn_mode, which the sections with both share.
type SecretManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	pCancel context.CancelFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	cfg     *config.CommonConfig
	// coreVisitor makes the access of a password.
	coreVisitor  func(password, mode string) coreapi.Visitor
	coreRelease  func()                     // gives back the shared session of coreVisitor
	visitors     map[string]coreapi.Visitor // by task password and conn_mode
	proxyServers []Closer
}

// NewSecretManager runs local servers until pCtx ends. With AutoReconnect
// off it gives up on the first failed server connection by calling pCancel;
// the owner of pCtx decides what follows.
func NewSecretManager(pCtx context.Context, pCancel context.CancelFunc, cfg *config.CommonConfig) *SecretManager {
	ctx, cancel := context.WithCancel(pCtx)
	mgr := &SecretManager{
		ctx:     ctx,
		cancel:  cancel,
		pCancel: pCancel,
		cfg:     cfg,
	}
	go func() {
		<-pCtx.Done()
		mgr.Close()
	}()
	return mgr
}

func (mgr *SecretManager) visitor(l *config.LocalServer, mode string) coreapi.Visitor {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	// A link has one conn_mode: sections differing in it have their own.
	key := l.Password + "\x00" + mode
	if v, ok := mgr.visitors[key]; ok {
		return v
	}
	if mgr.visitors == nil {
		mgr.visitors = make(map[string]coreapi.Visitor)
	}
	v := mgr.coreVisitor(l.Password, mode)
	mgr.visitors[key] = v
	return v
}

// secretAccess is the bridge of one local section's listeners: it opens
// every link on the section's visitor.
type secretAccess struct {
	mode string
	core coreapi.Visitor
	ctx  context.Context
	// compress is what the visitor asks of its nested streams, algorithms
	// in order of preference; the provider's Hello decides (§7.2).
	compress []string
}

// Session returns the access itself: there is one target whatever the client.
func (a *secretAccess) Session(int) conn.ClientSession { return a }

func (a *secretAccess) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	r := coreRequest(link)
	r.Compress = a.compress
	return a.core.OpenLink(a.ctx, r)
}

func (a *secretAccess) IsServer() bool { return false }

// LinkCert is the certificate npc presents on the encrypted links its
// visitors open.
func (a *secretAccess) LinkCert() *tls.Certificate { return localCert() }

// GlobalSource is nil: npc has no global source lists.
func (a *secretAccess) GlobalSource() *acl.Source { return nil }

func (a *secretAccess) CliProcess(*conn.Conn, string) {}

func (mgr *SecretManager) StartLocalServer(l *config.LocalServer) error {
	if mgr.ctx.Err() != nil {
		return errors.New("parent context canceled")
	}
	mode := l.ConnMode
	if mode == "" {
		mode = config.ConnAuto
	}
	// The nested stream compresses, not the listener: its Client asks for none.
	pb := &secretAccess{mode: mode, core: mgr.visitor(l, mode), ctx: mgr.ctx,
		compress: model.ResolveCompress(mgr.cfg.Client, model.CompressDefault)}
	ip := l.Ip
	if ip == "" {
		ip = "0.0.0.0"
	}

	task := &model.Tunnel{
		Port:     l.Port,
		ServerIp: ip,
		Status:   true,
		Client: &model.Client{
			Cnf: &model.Config{
				U: "",
				P: "",
			},
			Status:    true,
			IsConnect: true,
			RateLimit: 0,
			Flow:      &model.Flow{},
		},
		MultiAccount: localAccounts(l),
		HttpProxy:    true,
		Socks5Proxy:  true,
		Flow:         &model.Flow{},
		Target: &model.Target{
			TargetStr:  l.Target,
			LocalProxy: l.LocalProxy,
		},
	}

	switch l.Type {
	case config.LocalMixed:
		task.Mode = "mixProxy"
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		return mgr.serve(l.Type, "tcp", ip, l.Port, proxy.NewTunnelModeServer(proxy.ProcessMix, pb, task, true))
	case config.LocalTransparent:
		task.Mode = "p2pt" // the mode lib/proxy serves transparently
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		return mgr.serve(l.Type, "tcp", ip, l.Port, proxy.NewTunnelModeServer(proxy.HandleTrans, pb, task, true))
	}

	var errs []error
	if l.Type == config.LocalTCPUDP || l.Type == config.LocalTCP {
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		errs = append(errs, mgr.serve(l.Type, "tcp", ip, l.Port, proxy.NewTunnelModeServer(proxy.ProcessTunnel, pb, task, true)))
	}
	if l.Type == config.LocalTCPUDP || l.Type == config.LocalUDP {
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		errs = append(errs, mgr.serve(l.Type, "udp", ip, l.Port, proxy.NewUdpModeServer(pb, task, true)))
	}
	return errors.Join(errs...)
}

// localAccounts is the proxy accounts of a mixed section as the proxy reads
// them, nil for none.
func localAccounts(l *config.LocalServer) *model.MultiAccount {
	if len(l.Auth) == 0 {
		return nil
	}
	return &model.MultiAccount{AccountMap: l.Auth}
}

// localServer is a listener of a local section.
type localServer interface {
	Bind() error
	Start() error
	Close() error
}

// serve binds srv now, so that a port conflict is reported here, and serves
// it until the manager closes.
func (mgr *SecretManager) serve(mode, network, ip string, port int, srv localServer) error {
	route := mode + ":" + strconv.Itoa(port)
	listen := net.JoinHostPort(ip, strconv.Itoa(port))
	if err := srv.Bind(); err != nil {
		slogx.Emit(context.Background(), log, &logevent.ListenFailed, "route", route, "listen", listen, "network", network,
			"reason", logevent.NetReason(err), "err", err)
		_ = srv.Close()
		return err
	}
	mgr.mu.Lock()
	if mgr.ctx.Err() != nil {
		mgr.mu.Unlock()
		_ = srv.Close()
		return errors.New("parent context canceled")
	}
	mgr.proxyServers = append(mgr.proxyServers, srv)
	mgr.wg.Add(1)
	mgr.mu.Unlock()
	slogx.Emit(context.Background(), log, &logevent.Listening, "route", route, "listen", listen, "network", network)
	// The label names the local server in stack dumps and tracebacks.
	go pprof.Do(context.Background(), pprof.Labels("local", route+"/"+network), func(context.Context) {
		defer mgr.wg.Done()
		if err := srv.Start(); err != nil {
			log.Error("local server stopped", "route", route, "network", network, "err", err)
		}
	})
	return nil
}

// Close stops the listeners, the direct paths and the relay tunnel, and
// waits for the direct paths' goroutines.
func (mgr *SecretManager) Close() {
	mgr.cancel()
	mgr.mu.Lock()
	psList := mgr.proxyServers
	mgr.proxyServers = nil
	visitors := mgr.visitors
	mgr.visitors = nil
	release := mgr.coreRelease
	mgr.coreRelease = nil
	mgr.mu.Unlock()
	for _, v := range visitors {
		_ = v.Close()
	}
	if release != nil {
		defer release()
	}
	for _, srv := range psList {
		_ = srv.Close()
	}
	mgr.wg.Wait()
}
