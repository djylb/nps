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
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

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
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			return
		}
		if s.uuid == "" {
			s.uuid = uuid
		}
		err = SendType(c, legacy.WorkMain, s.uuid)
		if err != nil {
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			_ = c.Close()
			return
		}
		logs.Info("Successful connection with server %s", s.svrAddr)
		s.signal.Store(c)
	}
	//start a channel connection
	tunnel := s.newChan()
	if clientCaps().TunnelStreams {
		//c, err = NewConn(s.bridgeConnType, s.vKey, s.svrAddr, legacy.WorkMain, s.proxyUrl)
		if tunnel == nil {
			logs.Error("The tunnel is not connected")
			return
		}
		stream, err := tunnel.OpenStream(s.ctx)
		if err != nil {
			logs.Error("Failed to open the signal stream, possible version mismatch: %v", err)
			s.Close()
			return
		}
		if p, ok := stream.(interface{ SetPriority() }); ok {
			p.SetPriority()
		}
		stream = conn.SignalStream(stream)
		c := conn.NewConn(stream)
		if err = SendType(c, legacy.WorkMain, s.uuid); err != nil {
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			_ = stream.Close()
			return
		}
		s.signal.Store(c)
		logs.Info("Successful connection with server %s", s.svrAddr)
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
			logs.Error("Accept server data error %s, end this service", netx.NetErrorDetails(err, signal.Conn))
			return
		}
		switch flags {
		case legacy.P2PPunchStart:
			raw, err := legacy.ReadFrame(signal)
			if err != nil {
				logs.Warn("%v", err)
				return
			}
			var invite legacy.P2PInvite
			if err := json.Unmarshal(raw, &invite); err != nil {
				logs.Warn("decode p2p invite failed: %v", err)
				continue
			}
			go s.serveP2P(tunnel, invite)
		case legacy.UpdateRequest:
			if _, err := legacy.ReadFrame(signal); err != nil {
				logs.Warn("%v", err)
				return
			}
			handleUpdateRequest()
		case legacy.HealthSet:
			group, err := legacy.ReadHealthSet(signal)
			if err != nil {
				logs.Warn("%v", err)
				return
			}
			s.healthChecker.Load().setGroup(group)
		case legacy.HealthDel:
			group, err := legacy.ReadHealthDel(signal)
			if err != nil {
				logs.Warn("%v", err)
				return
			}
			s.healthChecker.Load().delGroup(group)
		default:
			// The bridge sends nothing else here: an unknown flag means the
			// stream is out of step, and reconnecting is the only way back.
			s.noteCloseReason("unexpected signal message " + strconv.Quote(flags))
			logs.Error("Unexpected message %q on the signal connection, reconnecting", flags)
			return
		}
	}
}

// newChan connects the mux tunnel and returns it; nil on failure.
func (s *TRPClient) newChan() legacy.Carrier {
	tunnel, uuid, server, err := dialServer(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP)
	if err != nil {
		logs.Error("Failed to connect to server %s error: %v", s.svrAddr, err)
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		return nil
	}
	if tunnel == nil {
		logs.Error("NewConn returned nil tunnel without error (server=%s tp=%s)", s.svrAddr, s.bridgeConnType)
		return nil
	}
	if s.uuid == "" {
		s.uuid = uuid
	}
	s.server = server
	err = SendType(tunnel, legacy.WorkChan, s.uuid)
	if err != nil {
		logs.Error("Failed to send type to server %s error: %v", s.svrAddr, err)
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
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
				logs.Warn("Accept error on mux: %v", err)
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
		logs.Error("get connection info from server error %v", err)
		return
	}
	//ack
	if lk.Option.NeedAck {
		if err := legacy.WriteAck(src, lk.Option.Timeout); err != nil {
			logs.Warn("write ACK failed: %v", err)
			_ = src.Close()
			return
		}
		logs.Trace("sent ACK before proceeding")
	}
	//socks5 udp
	if lk.ConnType == "udp5" {
		logs.Trace("new %s connection of udp5, remote address:%s", lk.ConnType, lk.RemoteAddr)
		if LocalIPForward {
			conn.HandleUdp5(s.ctx, src, lk.Option.Timeout, s.localIP)
		} else {
			conn.HandleUdp5(s.ctx, src, lk.Option.Timeout, "")
		}
		return
	}
	//file mode
	if lk.ConnType == "file" && s.fsm != nil {
		key := strings.TrimPrefix(strings.TrimSpace(lk.Host), "file://")
		vl, ok := s.fsm.GetListenerByKey(key)
		if !ok {
			logs.Warn("Fail to find file server: %s", key)
			_ = src.Close()
			return
		}
		rwc := conn.GetConn(src, lk.Crypt, lk.Compress, nil, false, false)
		// The virtual listener owns c from here on.
		//noinspection GoResourceLeak
		c := netx.WrapConn(rwc, src, netx.WithWrapParentClose())
		conn.ServeVirtual(vl, c)
		return
	}
	//host for target processing
	if lk.Host == "" {
		logs.Trace("new %s connection, remote address:%s", lk.ConnType, lk.RemoteAddr)
		_ = src.Close()
		return
	}
	lk.Host = common.FormatAddress(lk.Host)
	//connect to target if conn type is tcp or udp
	var targetConn net.Conn
	if LocalIPForward && s.localIP != "" && common.IsPublicHost(lk.Host) {
		dialer := net.Dialer{Timeout: lk.Option.Timeout}
		if lk.ConnType == "udp" {
			dialer.LocalAddr = common.BuildUDPBindAddr(s.localIP)
		} else {
			dialer.LocalAddr = common.BuildTCPBindAddr(s.localIP)
		}
		targetConn, err = dialer.DialContext(s.ctx, lk.ConnType, lk.Host)
	} else {
		// CopyWaitGroup closes targetConn.
		//noinspection GoResourceLeak
		targetConn, err = net.DialTimeout(lk.ConnType, lk.Host, lk.Option.Timeout)
	}
	hc := s.healthChecker.Load()
	if err != nil {
		logs.Warn("connect to %s error %v", lk.Host, err)
		if hc != nil {
			hc.dialFailed(lk.Host)
		}
		_ = src.Close()
	} else {
		// A UDP dial reaches nothing, so it says nothing about the target.
		if hc != nil && lk.ConnType != "udp" {
			hc.touch(lk.Host)
		}
		logs.Trace("new %s connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)
		isFramed := lk.ConnType == "udp" && clientCaps().FramedUDP
		//logs.Debug("%t", isFramed)
		conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, false, 0, nil, nil, false, isFramed)
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
	logs.Warn(
		"Disconnect summary event=disconnect_summary role=client server=%s bridge=%s uuid=%s uptime=%s tunnel_up=%t signal_up=%t reason=%q",
		s.svrAddr,
		s.bridgeConnType,
		s.uuid,
		uptime,
		tunnelUp,
		signalUp,
		reason,
	)
}

func (s *TRPClient) closeTunnel() {
	if p := s.tunnel.Swap(nil); p != nil {
		_ = (*p).Close()
	}
}
