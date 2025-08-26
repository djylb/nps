package proxy

import (
	"context"
	"net"
	"strings"
	"sync"
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
	ch       chan packet
	flowConn *conn.FlowConn
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
}

type UdpModeServer struct {
	*BaseServer
	listener    *net.UDPConn
	entries     sync.Map      // key: clientAddr.String(), value: *entry
	readTimeout time.Duration // idle timeout for back-channel reads
}

func NewUdpModeServer(bridge NetBridge, task *file.Tunnel, allowLocalProxy bool) *UdpModeServer {
	return &UdpModeServer{
		BaseServer:  NewBaseServer(bridge, task, allowLocalProxy),
		readTimeout: 60 * time.Second,
	}
}

func (s *UdpModeServer) Start() error {
	if s.Task.ServerIp == "" {
		s.Task.ServerIp = "0.0.0.0"
	}

	var err error
	s.listener, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(s.Task.ServerIp), Port: s.Task.Port})
	if err != nil {
		return err
	}

	for {
		buf := common.BufPoolUdp.Get().([]byte)
		n, addr, err := s.listener.ReadFromUDP(buf)
		if err != nil {
			common.PutBufPoolUdp(buf)
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			continue
		}

		// IP blacklist check
		if s.Bridge.IsServer() && (IsGlobalBlackIp(addr.String()) || common.IsBlackIp(addr.String(), s.Task.Client.VerifyKey, s.Task.Client.BlackIpList)) {
			common.PutBufPoolUdp(buf)
			continue
		}

		logs.Trace("New udp packet from client %d: %v", s.Task.Client.Id, addr)
		key := addr.String()
		v, loaded := s.entries.Load(key)
		if !loaded {
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
			common.PutBufPoolUdp(buf)
		case ent.ch <- packet{buf: buf, n: n}:
		default:
			common.PutBufPoolUdp(buf)
		}
	}

	return nil
}

func (s *UdpModeServer) clientWorker(addr *net.UDPAddr, ent *entry) {
	key := addr.String()
	defer func() {
		ent.cancel()
		s.entries.Delete(key)
		if ent.flowConn != nil {
			_ = ent.flowConn.Close()
		}
		for {
			select {
			case pkt := <-ent.ch:
				common.PutBufPoolUdp(pkt.buf)
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
	target := conn.GetConn(clientConn, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, nil, true, isLocal)
	ent.flowConn = conn.NewFlowConn(target, s.Task.Flow, s.Task.Client.Flow)

	go func() {
		buf := common.BufPoolUdp.Get().([]byte)
		defer common.PutBufPoolUdp(buf)

		for {
			select {
			case <-ent.ctx.Done():
				return
			default:
			}

			_ = clientConn.SetReadDeadline(time.Now().Add(s.readTimeout))
			nr, err := ent.flowConn.Read(buf)
			if err != nil {
				logs.Trace("back-channel read error or idle: %v", err)
				ent.cancel()
				return
			}
			if _, err := s.listener.WriteTo(buf[:nr], addr); err != nil {
				logs.Warn("error writing back to client: %v", err)
				ent.cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ent.ctx.Done():
			return
		case pkt, ok := <-ent.ch:
			if !ok {
				return
			}
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

			if _, err := ent.flowConn.Write(data); err != nil {
				common.PutBufPoolUdp(pkt.buf)
				ent.cancel()
				return
			}
			common.PutBufPoolUdp(pkt.buf)
		}
	}
}

func (s *UdpModeServer) Close() error {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.entries.Range(func(key, value interface{}) bool {
		ent := value.(*entry)
		ent.cancel()
		return true
	})
	return nil
}
