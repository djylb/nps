package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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

// ------------------------------
// P2PManager
// ------------------------------

type Closer interface{ Close() error }

// P2PManager runs the local sections of a visitor: their listeners, the
// relay (secret) tunnel and one direct path per P2P task.
type P2PManager struct {
	ctx          context.Context
	cancel       context.CancelFunc
	pCancel      context.CancelFunc
	mu           sync.Mutex
	dialMu       sync.Mutex // serializes connecting the visitor tunnel
	wg           sync.WaitGroup
	cfg          *config.CommonConfig
	uuid         string
	secretConn   legacy.Carrier
	server       serverInfo          // of the bridge connection carrying secretConn
	peers        map[string]*p2pPeer // by task password
	proxyServers []Closer
	mapper       *portmapper.Mapper
}

type P2pBridge struct {
	mgr     *P2PManager
	local   *config.LocalServer
	peer    *p2pPeer // nil when the section does not use P2P
	secret  bool
	timeout time.Duration
}

func NewP2pBridge(mgr *P2PManager, l *config.LocalServer) *P2pBridge {
	useP2P := l.Type != "secret" && !DisableP2P
	if useP2P && !clientCaps().P2P {
		p2pLog.Warn("p2p unavailable, relaying only", "reason", "version", "port", l.Port,
			"version", legacy.CoreVersion(protocolIndex), "min_version", legacy.CoreVersion(legacy.P2PMinIndex))
		useP2P = false
	}
	b := &P2pBridge{
		mgr:     mgr,
		local:   l,
		secret:  !useP2P || l.Fallback,
		timeout: 5 * time.Second,
	}
	if useP2P {
		b.peer = mgr.peer(l, b.secret)
	}
	return b
}

// NewP2PManager runs local servers until pCtx ends. With AutoReconnect off it
// gives up on the first failed server connection by calling pCancel; the
// owner of pCtx decides what follows.
func NewP2PManager(pCtx context.Context, pCancel context.CancelFunc, cfg *config.CommonConfig) *P2PManager {
	ctx, cancel := context.WithCancel(pCtx)
	mgr := &P2PManager{
		ctx:     ctx,
		cancel:  cancel,
		pCancel: pCancel,
		cfg:     cfg,
		peers:   make(map[string]*p2pPeer),
		mapper:  portmapper.New(portmapper.Config{Network: holepunch.SystemNetwork()}),
	}
	go func() {
		<-pCtx.Done()
		mgr.Close()
	}()
	return mgr
}

// peer returns the direct path of l's task, starting it on first use.
func (mgr *P2PManager) peer(l *config.LocalServer, relay bool) *p2pPeer {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	p, ok := mgr.peers[l.Password]
	if !ok {
		p = newP2PPeer(mgr, l.Password, fmt.Sprintf("P2P local port %d", l.Port))
		mgr.peers[l.Password] = p
		if mgr.ctx.Err() == nil {
			mgr.wg.Go(func() {
				p.run()
			})
		}
	}
	if relay {
		p.relay.Store(true)
	}
	return p
}

func (mgr *P2PManager) getUUID() string {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.uuid
}

// Session returns the bridge itself: there is one target whatever the client.
func (b *P2pBridge) Session(int) conn.ClientSession { return b }

// OpenLink opens a connection to the task's target: over the direct path
// when it is up, otherwise, or when opening a stream on it fails, over the
// relay if the section allows it. A direct path that fails a connection
// takes no new connections while it is checked; it is closed and punched
// again only when its checks fail too. Without a relay the connection waits
// up to the bridge timeout for a direct path.
func (b *P2pBridge) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	if link == nil {
		return nil, errors.New("link is nil")
	}
	var directErr error
	if b.peer != nil {
		t := b.peer.active()
		if t == nil && !b.secret {
			ctx, cancel := context.WithTimeout(b.mgr.ctx, b.timeout)
			t = b.peer.waitActive(ctx)
			cancel()
			if t == nil {
				return nil, errors.New("timeout waiting for the P2P direct path")
			}
		}
		if t != nil {
			c, err := t.openLink(b.mgr.ctx, *link)
			if err == nil {
				p2pLog.Log(context.Background(), slogx.LevelTrace, "using direct path", "target", link.Host)
				return c, nil
			}
			b.peer.suspect(t, err)
			if !b.secret {
				return nil, fmt.Errorf("P2P direct path: %w", err)
			}
			directErr = err
			slogx.Emit(context.Background(), p2pLog, &logevent.P2PPathChanged, "name", b.peer.label, "via", "relay", "target", link.Host, "reason", "direct_failed", "err", err)
		}
	}
	c, err := b.sendViaSecret(*link)
	if err != nil && directErr != nil {
		err = fmt.Errorf("%w (direct path: %v)", err, directErr)
	}
	return c, err
}

func (b *P2pBridge) sendViaSecret(link conn.Link) (net.Conn, error) {
	mgr := b.mgr
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
	if _, err := sc.Write([]byte(crypt.Md5(b.local.Password))); err != nil {
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
		if err := legacy.ReadAck(sc, b.timeout); err != nil {
			log.Debug("secret read ack failed", "err", err)
			return nil, err
		}
	}
	sendOK = true
	return sc, nil
}

func (b *P2pBridge) IsServer() bool {
	return false
}

// LinkCert is the certificate npc presents on the encrypted links its P2P
// visitors open.
func (b *P2pBridge) LinkCert() *tls.Certificate { return localCert() }

// GlobalSource is nil: npc has no global source lists.
func (b *P2pBridge) GlobalSource() *acl.Source { return nil }

func (b *P2pBridge) CliProcess(*conn.Conn, string) {
	// no-op
}

func (mgr *P2PManager) StartLocalServer(l *config.LocalServer) error {
	if mgr.ctx.Err() != nil {
		return errors.New("parent context canceled")
	}
	pb := NewP2pBridge(mgr, l)

	task := &model.Tunnel{
		Port:     l.Port,
		ServerIp: "0.0.0.0",
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
	case "p2ps":
		task.Mode = "mixProxy"
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		return mgr.serve(l.Type, "tcp", l.Port, proxy.NewTunnelModeServer(proxy.ProcessMix, pb, task, true))
	case "p2pt":
		task.Mode = "p2pt"
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		return mgr.serve(l.Type, "tcp", l.Port, proxy.NewTunnelModeServer(proxy.HandleTrans, pb, task, true))
	}

	var errs []error
	if l.TargetType == model.TargetAll || l.TargetType == model.TargetTCP {
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		errs = append(errs, mgr.serve(l.Type, "tcp", l.Port, proxy.NewTunnelModeServer(proxy.ProcessTunnel, pb, task, true)))
	}
	if l.TargetType == model.TargetAll || l.TargetType == model.TargetUDP {
		// serve owns the server and closes it on failure or shutdown.
		//noinspection GoResourceLeak
		errs = append(errs, mgr.serve(l.Type, "udp", l.Port, proxy.NewUdpModeServer(pb, task, true)))
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
func (mgr *P2PManager) serve(mode, network string, port int, srv localServer) error {
	route := mode + ":" + strconv.Itoa(port)
	listen := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
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
func (mgr *P2PManager) tunnelStream() (net.Conn, serverInfo, error) {
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
func (mgr *P2PManager) openTunnelConn() (net.Conn, serverInfo, error) {
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

// getSecretConn opens a relay (secret) connection.
func (mgr *P2PManager) getSecretConn() (net.Conn, error) {
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
func (mgr *P2PManager) Close() {
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
