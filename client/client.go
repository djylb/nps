package client

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	proxyUrl       string
	vKey           string
	uuid           string
	tunnel         any
	signal         *conn.Conn
	fsm            *FileServerManager
	ticker         *time.Ticker
	cnf            *config.Config
	disconnectTime int
	ctx            context.Context
	cancel         context.CancelFunc
	healthChecker  *HealthChecker
	once           sync.Once
}

// NewRPClient new client
func NewRPClient(svrAddr, vKey, bridgeConnType, proxyUrl, uuid string, cnf *config.Config, disconnectTime int, fsm *FileServerManager) *TRPClient {
	return &TRPClient{
		svrAddr:        svrAddr,
		vKey:           vKey,
		bridgeConnType: bridgeConnType,
		proxyUrl:       proxyUrl,
		uuid:           uuid,
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
		c, uuid, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl)
		if err != nil {
			HasFailed = true
			logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
			return
		}
		if s.uuid == "" {
			s.uuid = uuid
		}
		err = SendType(c, common.WORK_MAIN, s.uuid)
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
	s.newChan()
	if Ver > 4 {
		//c, err = NewConn(s.bridgeConnType, s.vKey, s.svrAddr, common.WORK_MAIN, s.proxyUrl)
		if s.tunnel == nil {
			logs.Error("The tunnel is not connected")
			return
		}
		switch t := s.tunnel.(type) {
		case *mux.Mux:
			mc, err := t.NewConn()
			if err != nil {
				logs.Error("Failed to get new connection, possible version mismatch: %v", err)
				s.Close()
				return
			}
			mc.SetPriority()
			c := conn.NewConn(mc)
			err = SendType(c, common.WORK_MAIN, s.uuid)
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
			err = SendType(c, common.WORK_MAIN, s.uuid)
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
	//msg connection, eg udp
	s.handleMain()
}

// handle main connection
func (s *TRPClient) handleMain() {
	defer s.Close()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		flags, err := s.signal.ReadFlag()
		if err != nil {
			logs.Error("Accept server data error %v, end this service", err)
			return
		}
		switch flags {
		case common.NEW_UDP_CONN:
			//read server udp addr and password
			if lAddr, err := s.signal.GetShortLenContent(); err != nil {
				logs.Warn("%v", err)
				return
			} else if pwd, err := s.signal.GetShortLenContent(); err == nil {
				rAddr := string(lAddr)
				remoteIP := net.ParseIP(common.GetIpByAddr(s.signal.RemoteAddr().String()))
				if remoteIP != nil && (remoteIP.IsPrivate() || remoteIP.IsLoopback() || remoteIP.IsLinkLocalUnicast()) {
					rAddr = common.BuildAddress(remoteIP.String(), strconv.Itoa(common.GetPortByAddr(rAddr)))
				}
				var localAddr string
				if strings.Contains(rAddr, "]:") {
					tmpConn, err := common.GetLocalUdp6Addr()
					if err != nil {
						logs.Error("%v", err)
						return
					}
					localAddr = tmpConn.LocalAddr().String()
				} else {
					tmpConn, err := common.GetLocalUdp4Addr()
					if err != nil {
						logs.Error("%v", err)
						return
					}
					localAddr = tmpConn.LocalAddr().String()
				}
				if !DisableP2P {
					go s.newUdpConn(localAddr, rAddr, string(pwd))
				}
			}
		}
	}
}

func (s *TRPClient) newUdpConn(localAddr, rAddr string, md5Password string) {
	var localConn net.PacketConn
	var err error
	var remoteAddress, role, mode, data string
	sendData := string(crypt.GetHMAC(s.vKey, crypt.GetCert().Certificate[0]))
	//logs.Debug("newUdpConn %s %s", localAddr, rAddr)
	if localConn, remoteAddress, localAddr, role, mode, data, err = handleP2PUdp(s.ctx, localAddr, rAddr, md5Password, common.WORK_P2P_PROVIDER, P2PMode, sendData); err != nil {
		logs.Error("handle P2P error: %v", err)
		return
	}
	defer localConn.Close()
	if mode == "" || mode != P2PMode {
		mode = common.CONN_KCP
	}
	wait := time.Duration(s.disconnectTime) * time.Second
	if wait <= 0 {
		wait = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	timer := time.AfterFunc(wait, func() {
		cancel()
	})
	defer timer.Stop()
	var kcpListener *kcp.Listener
	var quicListener *quic.Listener

	var preConnDone chan struct{}
	var preConnDoneOnce sync.Once
	done := func() {
		preConnDoneOnce.Do(func() {
			if preConnDone != nil {
				close(preConnDone)
			}
		})
	}

	if mode == common.CONN_QUIC {
		quicListener, err = quic.Listen(localConn, crypt.GetCertCfg(), QuicConfig)
		if err != nil {
			logs.Error("quic.Listen err: %v", err)
			return
		}
		defer quicListener.Close()
	} else {
		kcpListener, err = kcp.ServeConn(nil, 150, 3, localConn)
		if err != nil {
			logs.Error("kcp.ServeConn err: %v", err)
			return
		}
		defer kcpListener.Close()
		preConnDone = make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = kcpListener.Close()
			case <-preConnDone:
				return
			}
		}()
		defer done()
	}

	logs.Trace("start local p2p udp[%s] listen, role[%s], local address %s %v", mode, role, localAddr, localConn.LocalAddr())
	if data != "" {
		logs.Trace("P2P udp data is %s", data)
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch mode {
		case common.CONN_QUIC:
			sess, err := quicListener.Accept(ctx)
			if err != nil {
				logs.Warn("QUIC accept session error: %v", err)
				return
			}
			if sess.RemoteAddr().String() != remoteAddress {
				_ = sess.CloseWithError(0, "unexpected peer")
				continue
			}
			if !timer.Stop() {
				logs.Warn("QUIC pre-connection timer already fired")
				_ = sess.CloseWithError(0, "timer already fired")
				return
			}
			for {
				stream, err := sess.AcceptStream(ctx)
				if err != nil {
					logs.Trace("QUIC accept stream error: %v", err)
					_ = sess.CloseWithError(0, "accept stream error")
					return
				}
				c := conn.NewQuicStreamConn(stream, sess)
				go s.handleChan(c)
			}
		default: // KCP
			udpTunnel, err := kcpListener.AcceptKCP()
			if err != nil {
				logs.Error("acceptKCP failed on listener %v waiting for remote %s: %v", localConn.LocalAddr(), remoteAddress, err)
				return
			}
			if udpTunnel.RemoteAddr().String() != remoteAddress {
				_ = udpTunnel.Close()
				continue
			}
			if !timer.Stop() {
				logs.Warn("KCP pre-connection timer already fired")
				_ = udpTunnel.Close()
				return
			}
			done()
			conn.SetUdpSession(udpTunnel)
			logs.Trace("successful connection with client ,address %v", udpTunnel.RemoteAddr())
			//read link info from remote
			tunnel := mux.NewMux(udpTunnel, "kcp", s.disconnectTime, true)
			conn.Accept(tunnel, func(c net.Conn) {
				go s.handleChan(c)
			})
			logs.Trace("P2P connection closed, remote %v", udpTunnel.RemoteAddr())
			_ = tunnel.Close()
			return
		}
	}
}

// mux tunnel
func (s *TRPClient) newChan() {
	tunnel, uuid, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl)
	if err != nil {
		logs.Error("Failed to connect to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		return
	}
	if tunnel == nil {
		HasFailed = true
		logs.Error("NewConn returned nil tunnel without error (server=%s tp=%s)", s.svrAddr, s.bridgeConnType)
		return
	}
	if s.uuid == "" {
		s.uuid = uuid
	}
	err = SendType(tunnel, common.WORK_CHAN, s.uuid)
	if err != nil {
		logs.Error("Failed to send type to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		_ = tunnel.Close()
		return
	}
	if Ver > 4 && s.bridgeConnType == common.CONN_QUIC {
		qc, ok := tunnel.Conn.(*conn.QuicAutoCloseConn)
		if !ok {
			logs.Error("failed to get quic session")
			_ = tunnel.Close()
			return
		}
		sess := qc.GetSession()
		s.tunnel = sess
	} else {
		s.tunnel = mux.NewMux(tunnel.Conn, s.bridgeConnType, s.disconnectTime, true)
	}

	go func() {
		defer tunnel.Close()
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			var err error
			var src net.Conn
			switch t := s.tunnel.(type) {
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
				logs.Warn("Accept error on mux: %v", err)
				s.Close()
				return
			}
			go s.handleChan(src)
		}
	}()
}

func (s *TRPClient) handleChan(src net.Conn) {
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
		conn.HandleUdp5(s.ctx, src, lk.Option.Timeout)
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
	if targetConn, err := net.DialTimeout(lk.ConnType, lk.Host, lk.Option.Timeout); err != nil {
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
	s.ticker = time.NewTicker(time.Second * 5)
	for {
		select {
		case <-s.ticker.C:
			if s.isTunnelClosed() {
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
	if s.healthChecker != nil {
		s.healthChecker.Stop()
	}
	s.cancel()
	s.closeTunnel("close")
	if s.signal != nil {
		_ = s.signal.Close()
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}
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
