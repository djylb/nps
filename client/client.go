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
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

var log = slogx.Module("client")

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	carrier        connprofile.Candidate // the candidate that connected last
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
	directs        directGrants // direct paths served as provider, by grant
	cnf            *config.Config
	disconnectTime int
	ctx            context.Context
	cancel         context.CancelFunc
	healthChecker  atomic.Pointer[HealthChecker] // set after the tunnel starts taking links
	once           sync.Once
	startedNano    atomic.Int64
	reasonMu       sync.RWMutex
	closeReason    string // the disconnected reason word: lost, closed
	closeDetail    string // what was lost and how, for the err field
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

var pipesWarned sync.Once

// NotePipes takes the pipe mode of a connection. Several pipes per
// connection need zmux, so npc runs multi as single and says so once.
func NotePipes(mode string) {
	if mode == connprofile.PipesMulti {
		pipesWarned.Do(func() { log.Warn("multi pipes need zmux; running single") })
	}
}

var NowStatus atomic.Int32

func (s *TRPClient) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.Close()
	NowStatus.Store(0)
	if !clientCaps().TunnelStreams {
		c, uuid, info, err := dialServer(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP, s.candidateFailed)
		if err != nil {
			return
		}
		s.carrier = info.via
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
			s.noteCloseReason("lost", "signal read failed: "+err.Error())
			log.Debug("signal connection lost", "server", s.svrAddr, "reason", logevent.NetReason(err), "err", err)
			return
		}
		switch flags {
		case legacy.P2PPunchStart:
			raw, err := legacy.ReadFrame(signal)
			if err != nil {
				log.Debug("read secret invite failed", "server", s.svrAddr, "err", err)
				return
			}
			var invite legacy.P2PInvite
			if err := json.Unmarshal(raw, &invite); err != nil {
				log.Debug("decode secret invite failed", "server", s.svrAddr, "err", err)
				continue
			}
			go s.serveSecretDirect(tunnel, invite)
		case legacy.P2PRevokeGrant:
			raw, err := legacy.ReadFrame(signal)
			if err != nil {
				log.Debug("read grant revocation failed", "server", s.svrAddr, "err", err)
				return
			}
			var rv legacy.P2PRevoke
			if err := json.Unmarshal(raw, &rv); err != nil {
				log.Debug("decode grant revocation failed", "server", s.svrAddr, "err", err)
				continue
			}
			s.directs.revoke(rv.Grant)
		case legacy.UpdateRequest:
			if _, err := legacy.ReadFrame(signal); err != nil {
				log.Debug("read update request failed", "server", s.svrAddr, "err", err)
				return
			}
			handleUpdateRequest()
		case legacy.HealthSet:
			group, err := legacy.ReadHealthSet(signal)
			if err != nil {
				log.Debug("read health group failed", "server", s.svrAddr, "err", err)
				return
			}
			s.healthChecker.Load().setGroup(group)
		case legacy.HealthDel:
			group, err := legacy.ReadHealthDel(signal)
			if err != nil {
				log.Debug("read health group removal failed", "server", s.svrAddr, "err", err)
				return
			}
			s.healthChecker.Load().delGroup(group)
		default:
			// The bridge sends nothing else here: an unknown flag means the
			// stream is out of step, and reconnecting is the only way back.
			s.noteCloseReason("lost", "unexpected signal message "+strconv.Quote(flags))
			log.Debug("unexpected signal message", "server", s.svrAddr, "flag", flags)
			return
		}
	}
}

// newChan connects the mux tunnel and returns it; nil on failure.
func (s *TRPClient) newChan() legacy.Carrier {
	tunnel, uuid, server, err := dialServer(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP, s.candidateFailed)
	if err != nil {
		return nil
	}
	s.carrier = server.via
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
	tun := legacy.NewTunnel(tunnel, s.carrier.Carrier, clientCaps(), s.disconnectTime, true)
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
				s.noteCloseReason("lost", "tunnel accept failed: "+err.Error())
				log.Debug("tunnel lost", "server", s.carrier.Addr, "carrier", s.carrier.Carrier, "err", err)
				s.Close()
				return
			}
			go s.handleChan(src)
		}
	}()
	return tun
}

func (s *TRPClient) handleChan(src net.Conn) {
	if lk := readLink(src); lk != nil {
		s.serveLink(src, lk, nil)
	}
}

// readLink reads the link information that opens a stream and acknowledges
// it when asked; nil when src failed, which is then closed.
func readLink(src net.Conn) *conn.Link {
	// The wrapper only reads from src, which every path below closes or hands on.
	//noinspection GoResourceLeak
	lk, err := legacy.ReadLink(src)
	if err != nil || lk == nil {
		_ = src.Close()
		log.Debug("read link failed", "err", err)
		return nil
	}
	if lk.Option.NeedAck {
		if err := legacy.WriteAck(src, lk.Option.Timeout); err != nil {
			log.Debug("write ack failed", "err", err)
			_ = src.Close()
			return nil
		}
		log.Log(context.Background(), slogx.LevelTrace, "ack sent")
	}
	return lk
}

// serveLink connects src to what lk names. A link without a host goes to
// one of targets, when set (a secret task's targets on the direct path).
func (s *TRPClient) serveLink(src net.Conn, lk *conn.Link, targets *model.Target) {
	//socks5 udp
	if lk.ConnType == conn.LinkUDP5 {
		log.Debug("new link", "network", lk.ConnType, "addr", lk.RemoteAddr)
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
			log.Debug("file server not found", "name", key)
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
	var targetConn net.Conn
	var err error
	switch {
	case lk.Host != "":
		lk.Host = netaddr.FormatAddress(lk.Host)
		// CopyWaitGroup closes targetConn.
		//noinspection GoResourceLeak
		targetConn, err = s.dialTarget(lk)
	case targets != nil:
		err = targets.Dial(func(addr string) (bool, error) {
			lk.Host = netaddr.FormatAddress(addr)
			var derr error
			targetConn, derr = s.dialTarget(lk)
			return true, derr
		})
	default:
		log.Debug("link without target", "network", lk.ConnType, "addr", lk.RemoteAddr)
		_ = src.Close()
		return
	}
	if err != nil {
		log.Debug("dial target failed", "target", lk.Host, "err", err)
		_ = src.Close()
		return
	}
	log.Debug("new link", "network", lk.ConnType, "target", lk.Host, "addr", lk.RemoteAddr)
	isFramed := lk.ConnType == conn.LinkUDP && clientCaps().FramedUDP
	conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, nil, 0, nil, nil, false, isFramed)
}

// dialTarget connects to lk's host and reports the result to the health
// checks.
func (s *TRPClient) dialTarget(lk *conn.Link) (net.Conn, error) {
	var targetConn net.Conn
	var err error
	if LocalIPForward && s.localIP != "" && netaddr.IsPublicHost(lk.Host) {
		dialer := net.Dialer{Timeout: lk.Option.Timeout}
		if lk.ConnType == conn.LinkUDP {
			dialer.LocalAddr = netaddr.BuildUDPBindAddr(s.localIP)
		} else {
			dialer.LocalAddr = netaddr.BuildTCPBindAddr(s.localIP)
		}
		targetConn, err = dialer.DialContext(s.ctx, lk.ConnType, lk.Host)
	} else {
		targetConn, err = net.DialTimeout(lk.ConnType, lk.Host, lk.Option.Timeout)
	}
	if hc := s.healthChecker.Load(); hc != nil {
		if err != nil {
			hc.dialFailed(lk.Host)
		} else if lk.ConnType != conn.LinkUDP {
			// A UDP dial reaches nothing, so it says nothing about the target.
			hc.touch(lk.Host)
		}
	}
	return targetConn, err
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
	if reason, detail := s.snapshotCloseReason(); reason != "" {
		s.noteCloseReason(reason, detail)
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

// noteCloseReason records why the session ends, unless a reason is already
// recorded: reason is the disconnected reason word, detail the text for err.
func (s *TRPClient) noteCloseReason(reason, detail string) {
	if reason == "" {
		return
	}
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.closeReason == "" {
		s.closeReason, s.closeDetail = reason, detail
	}
}

func (s *TRPClient) getCloseReason() (string, string) {
	s.reasonMu.RLock()
	defer s.reasonMu.RUnlock()
	return s.closeReason, s.closeDetail
}

// snapshotCloseReason returns the reason word and detail of the end of the
// session: the first one noted, or what the tunnel or signal connection say.
func (s *TRPClient) snapshotCloseReason() (reason, detail string) {
	if reason, detail := s.getCloseReason(); reason != "" {
		return reason, detail
	}
	if tunnel := s.currentTunnel(); tunnel != nil {
		if d := tunnel.Reason(); d != "" {
			return "lost", d
		}
	}
	if signal := s.signal.Load(); signal != nil && signal.IsClosed() {
		return "lost", "signal connection closed"
	}
	return "", ""
}

func (s *TRPClient) logDisconnectSummary() {
	started := s.startedNano.Load()
	reason, detail := s.getCloseReason()
	signal := s.signal.Load()
	if started == 0 && signal == nil && s.currentTunnel() == nil && reason == "" {
		return
	}
	if reason == "" {
		reason = "closed"
	}
	uptime := time.Duration(0)
	if started > 0 {
		uptime = time.Since(time.Unix(0, started)).Round(time.Millisecond)
	}
	tunnelUp := !s.isTunnelClosed()
	signalUp := signal != nil && !signal.IsClosed()
	args := []any{"server", s.carrier.Addr, "carrier", s.carrier.Carrier, "uuid", s.uuid, "dur_ms", uptime.Milliseconds(),
		"tunnel_up", tunnelUp, "signal_up", signalUp, "reason", reason}
	if detail != "" {
		args = append(args, "err", detail)
	}
	slogx.Emit(context.Background(), log, &logevent.Disconnected, args...)
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

// candidateFailed writes connect_failed for a carrier candidate that did not
// connect.
func (s *TRPClient) candidateFailed(c connprofile.Candidate, err error) {
	s.emitConnectFailed(c, "dial", err)
}

// connectFailed writes connect_failed with how many attempts in a row have
// failed, this one included, for the candidate that connected last.
func (s *TRPClient) connectFailed(reason string, err error) {
	s.emitConnectFailed(s.carrier, reason, err)
}

func (s *TRPClient) emitConnectFailed(c connprofile.Candidate, reason string, err error) {
	args := []any{"server", c.Addr, "carrier", c.Carrier, "reason", reason, "attempt", s.attempts().Add(1)}
	if err != nil {
		args = append(args, "err", err)
	}
	slogx.Emit(s.ctxOrBackground(), log, &logevent.ConnectFailed, args...)
}

// connected writes connected, with the attempts it took, and resets them.
func (s *TRPClient) connected() {
	n := s.attempts().Swap(0)
	slogx.Emit(s.ctxOrBackground(), log, &logevent.Connected, "server", s.carrier.Addr, "carrier", s.carrier.Carrier, "attempts", n+1)
}

func (s *TRPClient) ctxOrBackground() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
