package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/quic-go/quic-go"
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
	tunnel         any
	signal         *conn.Conn
	fsm            *FileServerManager
	cnf            *config.Config
	disconnectTime int
	ctx            context.Context
	cancel         context.CancelFunc
	healthChecker  *HealthChecker
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

var NowStatus int
var HasFailed = false

func (s *TRPClient) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.Close()
	NowStatus = 0
	if Ver < 5 {
		c, uuid, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP)
		if err != nil {
			HasFailed = true
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			return
		}
		if s.uuid == "" {
			s.uuid = uuid
		}
		err = SendType(c, common.WorkMain, s.uuid)
		if err != nil {
			HasFailed = true
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			_ = c.Close()
			return
		}
		logs.Info("Successful connection with server %s", s.svrAddr)
		s.signal = c
	}
	//start a channel connection
	tunnel := s.newChan()
	if Ver > 4 {
		//c, err = NewConn(s.bridgeConnType, s.vKey, s.svrAddr, common.WorkMain, s.proxyUrl)
		if tunnel == nil {
			logs.Error("The tunnel is not connected")
			return
		}
		switch t := tunnel.(type) {
		case *mux.Mux:
			mc, err := t.NewConn()
			if err != nil {
				logs.Error("Failed to get new connection, possible version mismatch: %v", err)
				s.Close()
				return
			}
			mc.SetPriority()
			c := conn.NewConn(mc)
			err = SendType(c, common.WorkMain, s.uuid)
			if err != nil {
				logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
				_ = mc.Close()
				return
			}
			s.signal = c
		case *quic.Conn:
			stream, err := t.OpenStreamSync(s.ctx)
			if err != nil {
				logs.Error("Quic OpenStreamSync failed, retrying: %v", err)
				s.Close()
				return
			}
			sc := conn.NewQuicAutoCloseConn(stream, t)
			c := conn.NewConn(sc)
			err = SendType(c, common.WorkMain, s.uuid)
			if err != nil {
				logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
				_ = sc.Close()
				return
			}
			s.signal = c
		default:
			logs.Error("Unsupported tunnel type: %v", t)
			return
		}
		logs.Info("Successful connection with server %s", s.svrAddr)
	}
	//monitor the connection
	go s.ping()
	//start health check if it's open
	if s.cnf != nil && len(s.cnf.Healths) > 0 {
		s.healthChecker = NewHealthChecker(s.ctx, s.cnf.Healths, s.signal)
		s.healthChecker.Start()
	}
	NowStatus = 1
	s.markConnected()
	//msg connection, eg udp
	s.handleMain(tunnel)
}

// handle main connection
func (s *TRPClient) handleMain(tunnel any) {
	defer s.Close()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		flags, err := s.signal.ReadFlag()
		if err != nil {
			s.noteCloseReason("signal read failed: " + conn.DescribeNetError(err, s.signal.Conn))
			logs.Error("Accept server data error %s, end this service", conn.DescribeNetError(err, s.signal.Conn))
			return
		}
		switch flags {
		case common.P2PPunchStart:
			raw, err := s.signal.GetShortLenContent()
			if err != nil {
				logs.Warn("%v", err)
				return
			}
			var invite p2pproto.Invite
			if err := json.Unmarshal(raw, &invite); err != nil {
				logs.Warn("decode p2p invite failed: %v", err)
				continue
			}
			go s.serveP2P(tunnel, invite)
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
func (s *TRPClient) newChan() any {
	tunnel, uuid, server, err := dialServer(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP)
	if err != nil {
		logs.Error("Failed to connect to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		return nil
	}
	if tunnel == nil {
		HasFailed = true
		logs.Error("NewConn returned nil tunnel without error (server=%s tp=%s)", s.svrAddr, s.bridgeConnType)
		return nil
	}
	if s.uuid == "" {
		s.uuid = uuid
	}
	s.server = server
	err = SendType(tunnel, common.WorkChan, s.uuid)
	if err != nil {
		logs.Error("Failed to send type to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		_ = tunnel.Close()
		return nil
	}
	var tun any
	if Ver > 4 && s.bridgeConnType == common.ConnQUIC {
		qc, ok := tunnel.Conn.(*conn.QuicAutoCloseConn)
		if !ok {
			logs.Error("failed to get quic session")
			_ = tunnel.Close()
			return nil
		}
		tun = qc.GetSession()
	} else {
		tun = mux.NewMux(tunnel.Conn, s.bridgeConnType, s.disconnectTime, true)
	}
	s.tunnel = tun

	go func() {
		defer func() { _ = tunnel.Close() }()
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			var err error
			var src net.Conn
			switch t := tun.(type) {
			case *mux.Mux:
				src, err = t.Accept()
			case *quic.Conn:
				var stream *quic.Stream
				stream, err = t.AcceptStream(s.ctx)
				if err == nil {
					src = conn.NewQuicStreamConn(stream, t)
				}
			default:
				err = errors.New("unknown tunnel type")
			}

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
	lk, err := conn.NewConn(src).GetLinkInfo()
	if err != nil || lk == nil {
		_ = src.Close()
		logs.Error("get connection info from server error %v", err)
		return
	}
	//ack
	if lk.Option.NeedAck {
		if err := conn.WriteACK(src, lk.Option.Timeout); err != nil {
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
		c := conn.WrapConn(rwc, src)
		vl.ServeVirtual(c)
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
	if err != nil {
		logs.Warn("connect to %s error %v", lk.Host, err)
		_ = src.Close()
	} else {
		logs.Trace("new %s connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)
		isFramed := lk.ConnType == "udp" && Ver > 6
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
	if s.tunnel == nil {
		return true
	}
	switch t := s.tunnel.(type) {
	case *mux.Mux:
		return t.IsClosed()
	case *quic.Conn:
		return t.Context().Err() != nil
	default:
		return true
	}
}

func (s *TRPClient) Close() {
	s.once.Do(s.closing)
}

func (s *TRPClient) closing() {
	NowStatus = 0
	if reason := s.snapshotCloseReason(); reason != "" {
		s.noteCloseReason(reason)
	}
	s.logDisconnectSummary()
	if s.healthChecker != nil {
		s.healthChecker.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.closeTunnel("close")
	if s.signal != nil {
		_ = s.signal.Close()
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
	if s.tunnel != nil {
		switch t := s.tunnel.(type) {
		case *mux.Mux:
			if reason := t.CloseReason(); reason != "" {
				return reason
			}
		case *quic.Conn:
			if err := t.Context().Err(); err != nil {
				return "quic tunnel closed: " + err.Error()
			}
		}
	}
	if s.signal != nil && s.signal.IsClosed() {
		return "signal connection closed"
	}
	return ""
}

func (s *TRPClient) logDisconnectSummary() {
	started := s.startedNano.Load()
	reason := s.getCloseReason()
	if started == 0 && s.signal == nil && s.tunnel == nil && reason == "" {
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
	signalUp := s.signal != nil && !s.signal.IsClosed()
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

func (s *TRPClient) closeTunnel(err string) {
	if s.tunnel != nil {
		switch t := s.tunnel.(type) {
		case *mux.Mux:
			_ = t.Close()
		case *quic.Conn:
			_ = t.CloseWithError(0, err)
		default:
		}
		s.tunnel = nil
	}
}
