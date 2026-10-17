package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/proxy"
	"github.com/quic-go/quic-go"
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
	secretConn   any
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
	if useP2P && Ver < version.P2PMinIndex {
		logs.Warn("P2P needs protocol version %d or newer (running %d); local port %d relays only", version.P2PMinIndex, Ver, l.Port)
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
		if !AutoReconnect {
			os.Exit(1)
		}
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
			mgr.wg.Add(1)
			go func() {
				defer mgr.wg.Done()
				p.run()
			}()
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

// SendLinkInfo opens a connection to the task's target: over the direct path
// when it is up, otherwise, or when opening a stream on it fails, over the
// relay if the section allows it. A direct path that fails a connection
// takes no new connections while it is checked; it is closed and punched
// again only when its checks fail too. Without a relay the connection waits
// up to the bridge timeout for a direct path.
func (b *P2pBridge) SendLinkInfo(_ int, link *conn.Link, _ *file.Tunnel) (net.Conn, error) {
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
				logs.Trace("using the P2P direct path for %s", link.Host)
				return c, nil
			}
			b.peer.suspect(t, err)
			if !b.secret {
				return nil, fmt.Errorf("P2P direct path: %w", err)
			}
			directErr = err
			logs.Info("relaying connection to %s after the direct path failed", link.Host)
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
			logs.Trace("getSecretConn failed, retrying: %v", err)
		} else {
			logs.Trace("getSecretConn failed: %v", err)
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
		logs.Error("secret write password failed: %v", err)
		return nil, err
	}
	if _, err := conn.NewConn(sc).SendInfo(&link, ""); err != nil {
		logs.Trace("Secret SendInfo failed: %v", err)
		return nil, err
	}
	if link.Option.NeedAck {
		if err := conn.ReadACK(sc, b.timeout); err != nil {
			logs.Trace("Secret ReadACK failed: %v", err)
			return nil, err
		}
	}
	sendOK = true
	return sc, nil
}

func (b *P2pBridge) IsServer() bool {
	return false
}

func (b *P2pBridge) CliProcess(*conn.Conn, string) {
	// no-op
}

func (mgr *P2PManager) StartLocalServer(l *config.LocalServer) error {
	if mgr.ctx.Err() != nil {
		return errors.New("parent context canceled")
	}
	pb := NewP2pBridge(mgr, l)

	task := &file.Tunnel{
		Port:     l.Port,
		ServerIp: "0.0.0.0",
		Status:   true,
		Client: &file.Client{
			Cnf: &file.Config{
				U:        "",
				P:        "",
				Compress: mgr.cfg.Client.Cnf.Compress,
			},
			Status:    true,
			IsConnect: true,
			RateLimit: 0,
			Flow:      &file.Flow{},
		},
		HttpProxy:   true,
		Socks5Proxy: true,
		Flow:        &file.Flow{},
		Target: &file.Target{
			TargetStr:  l.Target,
			LocalProxy: l.LocalProxy,
		},
	}

	switch l.Type {
	case "p2ps":
		task.Mode = "mixProxy"
		return mgr.serve(fmt.Sprintf("http/socks5 proxy on port %d", l.Port), proxy.NewTunnelModeServer(proxy.ProcessMix, pb, task, true))
	case "p2pt":
		task.Mode = "p2pt"
		return mgr.serve(fmt.Sprintf("tcp trans on port %d", l.Port), proxy.NewTunnelModeServer(proxy.HandleTrans, pb, task, true))
	}

	var errs []error
	if l.TargetType == common.CONN_ALL || l.TargetType == common.CONN_TCP {
		errs = append(errs, mgr.serve(fmt.Sprintf("local tcp listener on port %d", l.Port), proxy.NewTunnelModeServer(proxy.ProcessTunnel, pb, task, true)))
	}
	if l.TargetType == common.CONN_ALL || l.TargetType == common.CONN_UDP {
		errs = append(errs, mgr.serve(fmt.Sprintf("local udp listener on port %d", l.Port), proxy.NewUdpModeServer(pb, task, true)))
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
func (mgr *P2PManager) serve(name string, srv localServer) error {
	if err := srv.Bind(); err != nil {
		logs.Error("%s failed to start: %v", name, err)
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
	logs.Info("%s started", name)
	go func() {
		defer mgr.wg.Done()
		if err := srv.Start(); err != nil {
			logs.Error("%s stopped: %v", name, err)
		}
	}()
	return nil
}

var errNoTunnel = errors.New("no visitor tunnel")

// tunnelStream opens a stream on the current visitor tunnel; a tunnel that
// cannot open one is dropped and closed.
func (mgr *P2PManager) tunnelStream() (net.Conn, serverInfo, error) {
	mgr.mu.Lock()
	tunnel, server := mgr.secretConn, mgr.server
	mgr.mu.Unlock()
	var c net.Conn
	var err error
	switch tun := tunnel.(type) {
	case nil:
		return nil, server, errNoTunnel
	case *mux.Mux:
		c, err = tun.NewConn()
	case *quic.Conn:
		var stream *quic.Stream
		if stream, err = tun.OpenStreamSync(mgr.ctx); err == nil {
			c = conn.NewQuicStreamConn(stream, tun)
		}
	default:
		err = errors.New("the tunnel type error")
	}
	if err == nil {
		return c, server, nil
	}
	mgr.mu.Lock()
	if mgr.secretConn == tunnel {
		mgr.secretConn = nil
	}
	mgr.mu.Unlock()
	closeTunnel(tunnel)
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
	pc, uuid, server, err := dialServer(mgr.cfg.Tp, mgr.cfg.VKey, mgr.cfg.Server, mgr.cfg.ProxyUrl, mgr.cfg.LocalIP)
	if err != nil {
		logs.Error("secret NewConn failed: %v", err)
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
	if Ver <= 5 {
		return pc, server, nil
	}
	if err = SendType(pc, common.WORK_VISITOR, uuid); err != nil {
		logs.Error("secret SendType failed: %v", err)
		_ = pc.Close()
		return nil, serverInfo{}, err
	}
	var c net.Conn
	var tunnel any
	if mgr.cfg.Tp == common.CONN_QUIC {
		qc, ok := pc.Conn.(*conn.QuicAutoCloseConn)
		if !ok {
			_ = pc.Close()
			return nil, serverInfo{}, errors.New("failed to get quic session")
		}
		sess := qc.GetSession()
		stream, err := sess.OpenStreamSync(mgr.ctx)
		if err != nil {
			logs.Error("secret OpenStreamSync failed: %v", err)
			_ = pc.Close()
			return nil, serverInfo{}, err
		}
		c, tunnel = conn.NewQuicStreamConn(stream, sess), sess
	} else {
		muxConn := mux.NewMux(pc.Conn, mgr.cfg.Tp, mgr.cfg.DisconnectTime, true)
		if c, err = muxConn.NewConn(); err != nil {
			logs.Error("secret muxConn failed: %v", err)
			_ = muxConn.Close()
			_ = pc.Close()
			return nil, serverInfo{}, err
		}
		tunnel = muxConn
	}
	mgr.mu.Lock()
	if err := mgr.ctx.Err(); err != nil {
		// Close ran meanwhile and will not see this tunnel.
		mgr.mu.Unlock()
		_ = c.Close()
		closeTunnel(tunnel)
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
	if err := SendType(conn.NewConn(c), common.WORK_SECRET, mgr.getUUID()); err != nil {
		logs.Error("secret SendType failed: %v", err)
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func closeTunnel(tunnel any) {
	switch tun := tunnel.(type) {
	case *mux.Mux:
		_ = tun.Close()
	case *quic.Conn:
		_ = tun.CloseWithError(0, "p2p close")
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
	closeTunnel(secretConn)
	mgr.wg.Wait()
	if mgr.mapper != nil {
		_ = mgr.mapper.Close()
	}
}
