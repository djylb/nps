package client

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/slogx"
)

type Closer interface{ Close() error }

// SecretManager runs the point-to-point access sections of a visitor: their
// listeners, the relay tunnel to nps and one peerLink per secret task
// (password), which the sections of that task share.
type SecretManager struct {
	ctx          context.Context
	cancel       context.CancelFunc
	pCancel      context.CancelFunc
	mu           sync.Mutex
	dialMu       sync.Mutex // serializes connecting the visitor tunnel
	wg           sync.WaitGroup
	cfg          *config.CommonConfig
	uuid         string
	secretConn   legacy.Carrier
	server       serverInfo           // of the bridge connection carrying secretConn
	links        map[string]*peerLink // by task password
	proxyServers []Closer
	mapper       *portmapper.Mapper
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
		links:   make(map[string]*peerLink),
		mapper:  portmapper.New(portmapper.Config{Network: holepunch.SystemNetwork()}),
	}
	go func() {
		<-pCtx.Done()
		mgr.Close()
	}()
	return mgr
}

// peerLink returns the link of l's task, created on first use, with a
// direct path started when l's mode wants one.
func (mgr *SecretManager) peerLink(l *config.LocalServer, mode string) *peerLink {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	p, ok := mgr.links[l.Password]
	if !ok {
		p = newPeerLink(mgr, l.Password, l.Type+":"+strconv.Itoa(l.Port))
		mgr.links[l.Password] = p
	}
	if mode != config.ConnRelay {
		p.wantDirect(mode == config.ConnAuto)
	}
	return p
}

func (mgr *SecretManager) getUUID() string {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.uuid
}

// secretAccess is the bridge of one local section's listeners: it opens
// every link on the section's peerLink in the section's mode.
type secretAccess struct {
	link *peerLink
	mode string
}

// Session returns the access itself: there is one target whatever the client.
func (a *secretAccess) Session(int) conn.ClientSession { return a }

func (a *secretAccess) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	return a.link.OpenLink(a.link.mgr.ctx, link, a.mode)
}

func (a *secretAccess) IsServer() bool { return false }

// LinkCert is the certificate npc presents on the encrypted links its
// visitors open.
func (a *secretAccess) LinkCert() *tls.Certificate { return localCert() }

// GlobalSource is nil: npc has no global source lists.
func (a *secretAccess) GlobalSource() *acl.Source { return nil }

func (a *secretAccess) CliProcess(*conn.Conn, string) {}

// connMode is l's connection mode, relay when this npc's protocol cannot
// punch.
func connMode(l *config.LocalServer) string {
	mode := l.ConnMode
	if mode == "" {
		mode = config.ConnAuto
	}
	if mode != config.ConnRelay && !clientCaps().P2P {
		p2pLog.Warn("direct path unavailable, relaying only", "reason", "version", "route", l.Type+":"+strconv.Itoa(l.Port),
			"version", legacy.CoreVersion(protocolIndex), "min_version", legacy.CoreVersion(legacy.P2PMinIndex))
		mode = config.ConnRelay
	}
	return mode
}

func (mgr *SecretManager) StartLocalServer(l *config.LocalServer) error {
	if mgr.ctx.Err() != nil {
		return errors.New("parent context canceled")
	}
	mode := connMode(l)
	pb := &secretAccess{link: mgr.peerLink(l, mode), mode: mode}
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
				U:        "",
				P:        "",
				Compress: mgr.cfg.Client.Cnf.Compress,
			},
			Status:    true,
			IsConnect: true,
			RateLimit: 0,
			Flow:      &model.Flow{},
		},
		HttpProxy:   true,
		Socks5Proxy: true,
		Flow:        &model.Flow{},
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

var errNoTunnel = errors.New("no visitor tunnel")

// tunnelStream opens a stream on the current visitor tunnel; a tunnel that
// cannot open one is dropped and closed.
func (mgr *SecretManager) tunnelStream() (net.Conn, serverInfo, error) {
	mgr.mu.Lock()
	tunnel, server := mgr.secretConn, mgr.server
	mgr.mu.Unlock()
	if tunnel == nil {
		return nil, server, errNoTunnel
	}
	c, err := tunnel.OpenStream(mgr.ctx)
	if err == nil {
		return c, server, nil
	}
	mgr.mu.Lock()
	if mgr.secretConn == tunnel {
		mgr.secretConn = nil
	}
	mgr.mu.Unlock()
	_ = tunnel.Close()
	return nil, server, err
}

// openTunnelConn opens a stream to the bridge on the visitor tunnel,
// connecting the tunnel first when there is none. Only one caller connects
// at a time; the others wait and use the tunnel it installs.
func (mgr *SecretManager) openTunnelConn() (net.Conn, serverInfo, error) {
	if c, server, err := mgr.tunnelStream(); err == nil {
		return c, server, nil
	}
	mgr.dialMu.Lock()
	defer mgr.dialMu.Unlock()
	if c, server, err := mgr.tunnelStream(); err == nil {
		return c, server, nil
	}
	if err := mgr.ctx.Err(); err != nil {
		return nil, serverInfo{}, err
	}
	// Every failure closes pc; otherwise it is returned or owned by the tunnel stored in mgr.
	//noinspection GoResourceLeak
	pc, uuid, server, err := dialServer(mgr.cfg.Tp, mgr.cfg.VKey, mgr.cfg.Server, mgr.cfg.ProxyUrl, mgr.cfg.LocalIP)
	if err != nil {
		log.Debug("secret connection to server failed", "server", mgr.cfg.Server, "err", err)
		if !AutoReconnect {
			mgr.pCancel()
		}
		return nil, serverInfo{}, err
	}
	mgr.mu.Lock()
	if mgr.uuid == "" {
		mgr.uuid = uuid
	} else {
		uuid = mgr.uuid
	}
	mgr.mu.Unlock()
	if !clientCaps().VisitorTunnel {
		return pc, server, nil
	}
	if err = SendType(pc, legacy.WorkVisitor, uuid); err != nil {
		log.Debug("secret send type failed", "err", err)
		_ = pc.Close()
		return nil, serverInfo{}, err
	}
	tunnel := legacy.NewTunnel(pc, mgr.cfg.Tp, clientCaps(), mgr.cfg.DisconnectTime, true)
	c, err := tunnel.OpenStream(mgr.ctx)
	if err != nil {
		log.Debug("secret open stream failed", "err", err)
		_ = tunnel.Close()
		_ = pc.Close()
		return nil, serverInfo{}, err
	}
	mgr.mu.Lock()
	if err := mgr.ctx.Err(); err != nil {
		// Close ran meanwhile and will not see this tunnel.
		mgr.mu.Unlock()
		_ = c.Close()
		_ = tunnel.Close()
		return nil, serverInfo{}, err
	}
	mgr.secretConn, mgr.server = tunnel, server
	mgr.mu.Unlock()
	return c, server, nil
}

// relay opens a link to the task of password over nps, which applies the
// task's policy to it.
func (mgr *SecretManager) relay(password string, link conn.Link, timeout time.Duration) (net.Conn, error) {
	sc, err := mgr.getSecretConn()
	if err != nil {
		if AutoReconnect {
			log.Debug("secret connection failed, retrying", "err", err)
		} else {
			log.Debug("secret connection failed", "err", err)
			mgr.pCancel()
		}
		return nil, err
	}
	sendOK := false
	defer func() {
		if !sendOK {
			_ = sc.Close()
		}
	}()
	if _, err := sc.Write([]byte(crypt.Md5(password))); err != nil {
		log.Debug("secret write password failed", "err", err)
		return nil, err
	}
	// The wrapper only writes to sc, which the deferred cleanup closes on failure.
	//noinspection GoResourceLeak
	if err := legacy.WriteLink(sc, &link); err != nil {
		log.Debug("secret send link failed", "err", err)
		return nil, err
	}
	if link.Option.NeedAck {
		if err := legacy.ReadAck(sc, timeout); err != nil {
			log.Debug("secret read ack failed", "err", err)
			return nil, err
		}
	}
	sendOK = true
	return sc, nil
}

// getSecretConn opens a relay (secret) connection.
func (mgr *SecretManager) getSecretConn() (net.Conn, error) {
	c, _, err := mgr.openTunnelConn()
	if err != nil {
		return nil, err
	}
	// The wrapper only writes to c, which is closed on failure and returned otherwise.
	//noinspection GoResourceLeak
	if err := SendType(conn.NewConn(c), legacy.WorkSecret, mgr.getUUID()); err != nil {
		log.Debug("secret send type failed", "err", err)
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Close stops the listeners, the direct paths and the relay tunnel, and
// waits for the direct paths' goroutines.
func (mgr *SecretManager) Close() {
	mgr.cancel()
	mgr.mu.Lock()
	psList := mgr.proxyServers
	secretConn := mgr.secretConn
	mgr.proxyServers = nil
	mgr.secretConn = nil
	mgr.mu.Unlock()
	for _, srv := range psList {
		_ = srv.Close()
	}
	if secretConn != nil {
		_ = secretConn.Close()
	}
	mgr.wg.Wait()
	if mgr.mapper != nil {
		_ = mgr.mapper.Close()
	}
}
