package client

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

var log = slogx.Module("client")

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	proxyUrl       string
	localIP        string
	vKey           string
	uuid           string
	mapper         *portmapper.Mapper
	server         serverInfo // of the tunnel's bridge connection; set before handleMain
	// tunnel and signal are read by the goroutines that close the client while
	// Start is still setting them up.
	tunnel         atomic.Pointer[legacy.Carrier]
	signal         atomic.Pointer[conn.Conn]
	fsm            *FileServerManager
	cnf            *config.Config
	disconnectTime int
	ctx            context.Context
	cancel         context.CancelFunc
	healthChecker  atomic.Pointer[HealthChecker] // set after the tunnel starts taking links
	once           sync.Once
	startedNano    atomic.Int64
	reasonMu       sync.RWMutex
	closeReason    string
}

// NewRPClient new client
func NewRPClient(svrAddr, vKey, bridgeConnType, proxyUrl, localIP, uuid string, cnf *config.Config, disconnectTime int, fsm *FileServerManager) *TRPClient {
	return &TRPClient{
		svrAddr:        svrAddr,
		vKey:           vKey,
		bridgeConnType: bridgeConnType,
		proxyUrl:       proxyUrl,
		localIP:        localIP,
		uuid:           uuid,
		mapper:         portmapper.New(portmapper.Config{Network: holepunch.SystemNetwork()}),
		cnf:            cnf,
		disconnectTime: disconnectTime,
		fsm:            fsm,
		once:           sync.Once{},
	}
}

var NowStatus atomic.Int32

func (s *TRPClient) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.Close()
	NowStatus.Store(0)
	if !clientCaps().TunnelStreams {
		c, uuid, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP)
		if err != nil {
			s.connectFailed("dial", err)
			return
		}
		if s.uuid == "" {
			s.uuid = uuid
		}
		err = SendType(c, legacy.WorkMain, s.uuid)
		if err != nil {
			s.connectFailed("send_type", err)
			_ = c.Close()
			return
		}
		s.connected()
		s.signal.Store(c)
	}
	//start a channel connection
	tunnel := s.newChan()
	if clientCaps().TunnelStreams {
		//c, err = NewConn(s.bridgeConnType, s.vKey, s.svrAddr, legacy.WorkMain, s.proxyUrl)
		if tunnel == nil {
			log.Debug("tunnel not connected", "server", s.svrAddr)
			return
		}
		stream, err := tunnel.OpenStream(s.ctx)
		if err != nil {
			s.connectFailed("signal_stream", err)
			s.Close()
			return
		}
		if p, ok := stream.(interface{ SetPriority() }); ok {
			p.SetPriority()
		}
		stream = conn.SignalStream(stream)
		c := conn.NewConn(stream)
		if err = SendType(c, legacy.WorkMain, s.uuid); err != nil {
			s.connectFailed("send_type", err)
			_ = stream.Close()
			return
		}
		s.signal.Store(c)
		s.connected()
	}
	//monitor the connection
	go s.ping()
	// the checks of the groups the server gives
	s.healthChecker.Store(NewHealthChecker(s.ctx, legacyHealthSink{signal: s.signal.Load()}))
	NowStatus.Store(1)
	s.markConnected()
	//msg connection, eg udp
	s.handleMain(tunnel)
}

// handle main connection
func (s *TRPClient) handleMain(tunnel legacy.Carrier) {
	defer s.Close()
	signal := s.signal.Load()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		flags, err := legacy.ReadFlag(signal)
		if err != nil {
			s.noteCloseReason("signal read failed: " + netx.NetErrorDetails(err, signal.Conn).String())
			log.Warn("signal connection lost", "server", s.svrAddr, "err", netx.NetErrorDetails(err, signal.Conn).String())
			return
		}
		switch flags {
		case legacy.P2PPunchStart:
			raw, err := legacy.ReadFrame(signal)
			if err != nil {
				log.Warn("read p2p invite failed", "server", s.svrAddr, "err", err)
				return
			}
			var invite legacy.P2PInvite
			if err := json.Unmarshal(raw, &invite); err != nil {
				log.Warn("decode p2p invite failed", "server", s.svrAddr, "err", err)
				continue
			}
			go s.serveP2P(tunnel, invite)
		case legacy.UpdateRequest:
			if _, err := legacy.ReadFrame(signal); err != nil {
				log.Warn("read update request failed", "server", s.svrAddr, "err", err)
				return
			}
			handleUpdateRequest()
		case legacy.HealthSet:
			group, err := legacy.ReadHealthSet(signal)
			if err != nil {
				log.Warn("read health group failed", "server", s.svrAddr, "err", err)
				return
			}
			s.healthChecker.Load().setGroup(group)
		case legacy.HealthDel:
			group, err := legacy.ReadHealthDel(signal)
			if err != nil {
				log.Warn("read health group removal failed", "server", s.svrAddr, "err", err)
				return
			}
			s.healthChecker.Load().delGroup(group)
		default:
			// The bridge sends nothing else here: an unknown flag means the
			// stream is out of step, and reconnecting is the only way back.
			s.noteCloseReason("unexpected signal message " + strconv.Quote(flags))
			log.Warn("unexpected signal message, reconnecting", "server", s.svrAddr, "flag", flags)
			return
		}
	}
}

// newChan connects the mux tunnel and returns it; nil on failure.
func (s *TRPClient) newChan() legacy.Carrier {
	tunnel, uuid, server, err := dialServer(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP)
	if err != nil {
		s.connectFailed("dial", err)
		return nil
	}
	if tunnel == nil {
		s.connectFailed("no_tunnel", nil)
		return nil
	}
	if s.uuid == "" {
		s.uuid = uuid
	}
	s.server = server
	err = SendType(tunnel, legacy.WorkChan, s.uuid)
	if err != nil {
		s.connectFailed("send_type", err)
		_ = tunnel.Close()
		return nil
	}
	tun := legacy.NewTunnel(tunnel, s.bridgeConnType, clientCaps(), s.disconnectTime, true)
	s.tunnel.Store(&tun)

	go func() {
		defer func() { _ = tunnel.Close() }()
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			src, err := tun.AcceptStream(s.ctx)
			if err != nil {
				s.noteCloseReason("tunnel accept failed: " + err.Error())
				log.Warn("tunnel lost", "server", s.svrAddr, "carrier", s.bridgeConnType, "err", err)
				s.Close()
				return
			}
			go s.handleChan(src)
		}
	}()
	return tun
}

func (s *TRPClient) handleChan(src net.Conn) {
	// The wrapper only reads from src, which every path below closes or hands on.
	//noinspection GoResourceLeak
	lk, err := legacy.ReadLink(src)
	if err != nil || lk == nil {
		_ = src.Close()
		log.Debug("read link failed", "err", err)
		return
	}
	//ack
	if lk.Option.NeedAck {
		if err := legacy.WriteAck(src, lk.Option.Timeout); err != nil {
			log.Debug("write ack failed", "err", err)
			_ = src.Close()
			return
		}
		log.Log(context.Background(), slogx.LevelTrace, "ack sent")
	}
	//socks5 udp
	if lk.ConnType == conn.LinkUDP5 {
		log.Debug("new link", "type", lk.ConnType, "addr", lk.RemoteAddr)
		if LocalIPForward {
			conn.HandleUdp5(s.ctx, src, lk.Option.Timeout, s.localIP)
		} else {
			conn.HandleUdp5(s.ctx, src, lk.Option.Timeout, "")
		}
		return
	}
	//file mode
	if lk.ConnType == conn.LinkFile && s.fsm != nil {
		key := strings.TrimPrefix(strings.TrimSpace(lk.Host), "file://")
		vl, ok := s.fsm.GetListenerByKey(key)
		if !ok {
			log.Info("file server not found", "key", key)
			_ = src.Close()
			return
		}
		rwc := conn.GetConn(src, lk.Crypt, lk.Compress, nil, nil, false)
		// The virtual listener owns c from here on.
		//noinspection GoResourceLeak
		c := netx.WrapConn(rwc, src, netx.WithWrapParentClose())
		conn.ServeVirtual(vl, c)
		return
	}
	//host for target processing
	if lk.Host == "" {
		log.Debug("link without target", "type", lk.ConnType, "addr", lk.RemoteAddr)
		_ = src.Close()
		return
	}
	lk.Host = netaddr.FormatAddress(lk.Host)
	//connect to target if conn type is tcp or udp
	var targetConn net.Conn
	if LocalIPForward && s.localIP != "" && netaddr.IsPublicHost(lk.Host) {
		dialer := net.Dialer{Timeout: lk.Option.Timeout}
		if lk.ConnType == conn.LinkUDP {
			dialer.LocalAddr = netaddr.BuildUDPBindAddr(s.localIP)
		} else {
			dialer.LocalAddr = netaddr.BuildTCPBindAddr(s.localIP)
		}
		targetConn, err = dialer.DialContext(s.ctx, lk.ConnType, lk.Host)
	} else {
		// CopyWaitGroup closes targetConn.
		//noinspection GoResourceLeak
		targetConn, err = net.DialTimeout(lk.ConnType, lk.Host, lk.Option.Timeout)
	}
	hc := s.healthChecker.Load()
	if err != nil {
		log.Debug("dial target failed", "target", lk.Host, "err", err)
		if hc != nil {
			hc.dialFailed(lk.Host)
		}
		_ = src.Close()
	} else {
		// A UDP dial reaches nothing, so it says nothing about the target.
		if hc != nil && lk.ConnType != conn.LinkUDP {
			hc.touch(lk.Host)
		}
		log.Debug("new link", "type", lk.ConnType, "target", lk.Host, "addr", lk.RemoteAddr)
		isFramed := lk.ConnType == conn.LinkUDP && clientCaps().FramedUDP
		conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, nil, 0, nil, nil, false, isFramed)
	}
}

// Whether the monitor channel is closed
func (s *TRPClient) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if s.isTunnelClosed() {
				s.noteCloseReason(s.snapshotCloseReason())
				s.Close()
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *TRPClient) isTunnelClosed() bool {
	tunnel := s.currentTunnel()
	return tunnel == nil || tunnel.Closed()
}

func (s *TRPClient) currentTunnel() legacy.Carrier {
	if p := s.tunnel.Load(); p != nil {
		return *p
	}
	return nil
}

func (s *TRPClient) Close() {
	s.once.Do(s.closing)
}

func (s *TRPClient) closing() {
	NowStatus.Store(0)
	if reason := s.snapshotCloseReason(); reason != "" {
		s.noteCloseReason(reason)
	}
	s.logDisconnectSummary()
	if s.cancel != nil {
		s.cancel()
	}
	s.closeTunnel()
	if signal := s.signal.Load(); signal != nil {
		_ = signal.Close()
	}
	// After the signal connection, whose close ends a report being written.
	if hc := s.healthChecker.Load(); hc != nil {
		hc.Stop()
	}
	if s.mapper != nil {
		_ = s.mapper.Close()
	}
}

func (s *TRPClient) markConnected() {
	s.startedNano.Store(time.Now().UnixNano())
}

func (s *TRPClient) noteCloseReason(reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.closeReason == "" {
		s.closeReason = reason
	}
}

func (s *TRPClient) getCloseReason() string {
	s.reasonMu.RLock()
	defer s.reasonMu.RUnlock()
	return s.closeReason
}

func (s *TRPClient) snapshotCloseReason() string {
	if reason := s.getCloseReason(); reason != "" {
		return reason
	}
	if tunnel := s.currentTunnel(); tunnel != nil {
		if reason := tunnel.Reason(); reason != "" {
			return reason
		}
	}
	if signal := s.signal.Load(); signal != nil && signal.IsClosed() {
		return "signal connection closed"
	}
	return ""
}

func (s *TRPClient) logDisconnectSummary() {
	started := s.startedNano.Load()
	reason := s.getCloseReason()
	signal := s.signal.Load()
	if started == 0 && signal == nil && s.currentTunnel() == nil && reason == "" {
		return
	}
	if reason == "" {
		reason = "close requested"
	}
	uptime := time.Duration(0)
	if started > 0 {
		uptime = time.Since(time.Unix(0, started)).Round(time.Millisecond)
	}
	tunnelUp := !s.isTunnelClosed()
	signalUp := signal != nil && !signal.IsClosed()
	slogx.Emit(context.Background(), log, &logevent.Disconnected,
		"server", s.svrAddr,
		"carrier", s.bridgeConnType,
		"uuid", s.uuid,
		"dur_ms", uptime.Milliseconds(),
		"tunnel_up", tunnelUp,
		"signal_up", signalUp,
		"reason", reason,
	)
}

func (s *TRPClient) closeTunnel() {
	if p := s.tunnel.Swap(nil); p != nil {
		_ = (*p).Close()
	}
}

// connectAttempts counts the failed connection attempts in a row per server
// and connection type, across the clients the reconnect loop creates.
var connectAttempts sync.Map // string -> *atomic.Int64

func (s *TRPClient) attempts() *atomic.Int64 {
	v, _ := connectAttempts.LoadOrStore(s.bridgeConnType+"://"+s.svrAddr, new(atomic.Int64))
	return v.(*atomic.Int64)
}

// connectFailed writes connect_failed with how many attempts in a row have
// failed, this one included.
func (s *TRPClient) connectFailed(reason string, err error) {
	args := []any{"server", s.svrAddr, "carrier", s.bridgeConnType, "reason", reason, "attempt", s.attempts().Add(1)}
	if err != nil {
		args = append(args, "err", err)
	}
	slogx.Emit(s.ctxOrBackground(), log, &logevent.ConnectFailed, args...)
}

// connected writes connected, with the attempts it took, and resets them.
func (s *TRPClient) connected() {
	n := s.attempts().Swap(0)
	slogx.Emit(s.ctxOrBackground(), log, &logevent.Connected, "server", s.svrAddr, "carrier", s.bridgeConnType, "attempts", n+1)
}

func (s *TRPClient) ctxOrBackground() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
