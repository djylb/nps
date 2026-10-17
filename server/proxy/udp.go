package proxy

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type packet struct {
	buf []byte
	n   int
}

type entry struct {
	ch         chan packet
	conn       net.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	once       sync.Once
	lastActive atomic.Int64 // monotonic time (since udpClock) of the latest packet in either direction
}

// udpClock anchors entry activity stamps to the monotonic clock.
var udpClock = time.Now()

func (e *entry) touch() {
	e.lastActive.Store(int64(time.Since(udpClock)))
}

func (e *entry) idleFor() time.Duration {
	return time.Since(udpClock) - time.Duration(e.lastActive.Load())
}

type UdpModeServer struct {
	*BaseServer
	listener    *net.UDPConn
	entries     sync.Map      // key: clientAddr.String(), value: *entry
	readTimeout time.Duration // idle timeout of a session, counted over both directions
}

func NewUdpModeServer(bridge NetBridge, task *file.Tunnel, allowLocalProxy bool) *UdpModeServer {
	return &UdpModeServer{
		BaseServer:  NewBaseServer(bridge, task, allowLocalProxy),
		readTimeout: 60 * time.Second,
	}
}

// Bind opens the UDP socket.
func (s *UdpModeServer) Bind() error {
	return s.bindLocked(func() error {
		if s.listener != nil {
			return nil
		}
		if s.Task.ServerIp == "" {
			s.Task.ServerIp = "0.0.0.0"
		}
		listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(s.Task.ServerIp), Port: s.Task.Port})
		if err != nil {
			return err
		}
		s.listener = listener
		return nil
	})
}

func (s *UdpModeServer) Start() error {
	if err := s.Bind(); err != nil {
		return err
	}

	for {
		buf := common.BufPoolUdp.Get()
		n, addr, err := s.listener.ReadFromUDP(buf)
		if err != nil {
			common.BufPoolUdp.Put(buf)
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			continue
		}

		// IP blacklist check
		if s.Bridge.IsServer() && (IsGlobalBlackIp(addr.String()) || common.IsBlackIp(addr.String(), s.Task.Client.VerifyKey, s.Task.Client.BlackIpList)) {
			common.BufPoolUdp.Put(buf)
			continue
		}

		//logs.Trace("New udp packet from client %d: %v", s.Task.Client.Id, addr)
		key := addr.String()
		v, loaded := s.entries.Load(key)
		if !loaded {
			logs.Trace("New udp packet from client %d: %v", s.Task.Client.Id, addr)
			ctx, cancel := context.WithCancel(context.Background())
			ent := &entry{
				ch:     make(chan packet, 1024),
				ctx:    ctx,
				cancel: cancel,
			}
			s.entries.Store(key, ent)
			go s.clientWorker(addr, ent)
			v = ent
		}
		ent := v.(*entry)

		select {
		case <-ent.ctx.Done():
			common.BufPoolUdp.Put(buf)
		case ent.ch <- packet{buf: buf, n: n}:
		default:
			common.BufPoolUdp.Put(buf)
		}
	}

	return nil
}

func (s *UdpModeServer) clientWorker(addr *net.UDPAddr, ent *entry) {
	key := addr.String()
	defer func() {
		ent.cancel()
		s.entries.CompareAndDelete(key, ent)
		if ent.conn != nil {
			_ = ent.conn.Close()
		}
		for {
			select {
			case pkt := <-ent.ch:
				common.BufPoolUdp.Put(pkt.buf)
			default:
				return
			}
		}
	}()

	if s.Bridge.IsServer() {
		if err := s.CheckFlowAndConnNum(s.Task.Client); err != nil {
			logs.Warn("client Id %d, task Id %d flow/conn limit: %v", s.Task.Client.Id, s.Task.Id, err)
			return
		}
		if err := conn.CheckFlowLimits(s.Task.Flow, "Task", time.Now()); err != nil {
			logs.Warn("client Id %d, task Id %d flow/conn limit: %v", s.Task.Client.Id, s.Task.Id, err)
			return
		}
		defer s.Task.Client.CutConn()
		s.Task.AddConn()
		defer s.Task.CutConn()
	}
	isLocal := s.AllowLocalProxy && s.Task.Target.LocalProxy || s.Task.Client.Id < 0
	link := conn.NewLink(common.CONN_UDP, s.Task.Target.TargetStr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, key, isLocal)
	clientConn, err := s.Bridge.SendLinkInfo(s.Task.Client.Id, link, s.Task)
	if err != nil {
		logs.Trace("SendLinkInfo error: %v", err)
		return
	}
	target := conn.GetConn(clientConn, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, s.Task.Client.Rate, true, isLocal)
	flowConn := conn.NewFlowConn(target, s.Task.Flow, s.Task.Client.Flow)
	if isLocal && s.Bridge.IsServer() {
		ent.conn = flowConn
	} else {
		ent.conn = conn.WrapFramed(flowConn)
	}

	// The back-channel reader blocks without a deadline; it is released by
	// closing ent.conn when the worker returns.
	go func() {
		buf := common.BufPoolUdp.Get()
		defer common.BufPoolUdp.Put(buf)

		for {
			nr, err := ent.conn.Read(buf)
			if err != nil {
				logs.Trace("back-channel read error: %v", err)
				ent.cancel()
				return
			}
			ent.touch()
			if _, err := s.listener.WriteTo(buf[:nr], addr); err != nil {
				logs.Warn("error writing back to client: %v", err)
				ent.cancel()
				return
			}
		}
	}()

	// The session ends once neither direction has carried a packet for readTimeout.
	ent.touch()
	idle := time.NewTimer(s.readTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ent.ctx.Done():
			return
		case <-idle.C:
			remaining := s.readTimeout - ent.idleFor()
			if remaining <= 0 {
				logs.Trace("udp session %s idle for %v", key, s.readTimeout)
				return
			}
			idle.Reset(remaining)
		case pkt, ok := <-ent.ch:
			if !ok {
				return
			}
			ent.touch()
			data := pkt.buf[:pkt.n]
			ent.once.Do(func() {
				if s.Task.Target.ProxyProtocol != 0 {
					hdr := conn.BuildProxyProtocolHeaderByAddr(addr, &net.UDPAddr{Port: s.Task.Port}, s.Task.Target.ProxyProtocol)
					hdrLen := len(hdr)
					if hdrLen > 0 {
						mergeBuf := make([]byte, hdrLen+len(data))
						copy(mergeBuf, hdr)
						copy(mergeBuf[hdrLen:], data)
						data = mergeBuf
					}
				}
			})

			if _, err := ent.conn.Write(data); err != nil {
				common.BufPoolUdp.Put(pkt.buf)
				ent.cancel()
				return
			}
			common.BufPoolUdp.Put(pkt.buf)
		}
	}
}

func (s *UdpModeServer) Close() error {
	var listener *net.UDPConn
	s.markClosed(func() { listener = s.listener })
	if listener != nil {
		_ = listener.Close()
	}
	s.entries.Range(func(key, value interface{}) bool {
		ent := value.(*entry)
		ent.cancel()
		return true
	})
	return nil
}
