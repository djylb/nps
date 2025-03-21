package proxy

import (
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/beego/beego"
	"github.com/beego/beego/logs"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type UdpModeServer struct {
	BaseServer
	addrMap  sync.Map
	listener *net.UDPConn
}

func NewUdpModeServer(bridge *bridge.Bridge, task *file.Tunnel) *UdpModeServer {
	allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy")
	s := new(UdpModeServer)
	s.bridge = bridge
	s.task = task
	s.allowLocalProxy = allowLocalProxy
	return s
}

// 开始
func (s *UdpModeServer) Start() error {
	var err error
	if s.task.ServerIp == "" {
		s.task.ServerIp = "0.0.0.0"
	}
	s.listener, err = net.ListenUDP("udp", &net.UDPAddr{net.ParseIP(s.task.ServerIp), s.task.Port, ""})
	if err != nil {
		return err
	}
	for {
		buf := common.BufPoolUdp.Get().([]byte)
		n, addr, err := s.listener.ReadFromUDP(buf)
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			continue
		}

		// 判断访问地址是否在全局黑名单内
		if IsGlobalBlackIp(addr.String()) {
			break
		}

		// 判断访问地址是否在黑名单内
		if common.IsBlackIp(addr.String(), s.task.Client.VerifyKey, s.task.Client.BlackIpList) {
			break
		}

		logs.Trace("New udp connection,client %d,remote address %s", s.task.Client.Id, addr)
		go s.process(addr, buf[:n])
	}
	return nil
}

func (s *UdpModeServer) process(addr *net.UDPAddr, data []byte) {
	if v, ok := s.addrMap.Load(addr.String()); ok {
		clientConn, ok := v.(io.ReadWriteCloser)
		if ok {
			_, err := clientConn.Write(data)
			if err != nil {
				logs.Warn(err)
				return
			}

			dataLength := int64(len(data))
			s.task.Flow.Add(dataLength, 0)
			s.task.Client.Flow.Add(dataLength, dataLength)
		}
	} else {
		if err := s.CheckFlowAndConnNum(s.task.Client); err != nil {
			logs.Warn("client id %d, task id %d,error %s, when udp connection", s.task.Client.Id, s.task.Id, err.Error())
			return
		}
		defer s.task.Client.CutConn()
		link := conn.NewLink(common.CONN_UDP, s.task.Target.TargetStr, s.task.Client.Cnf.Crypt, s.task.Client.Cnf.Compress, addr.String(), s.allowLocalProxy && s.task.Target.LocalProxy)
		clientConn, err := s.bridge.SendLinkInfo(s.task.Client.Id, link, s.task)
		if err != nil {
			return
		}
		target := conn.GetConn(clientConn, s.task.Client.Cnf.Crypt, s.task.Client.Cnf.Compress, nil, true)
		s.addrMap.Store(addr.String(), target)
		defer target.Close()

		_, err = target.Write(data)
		if err != nil {
			logs.Warn(err)
			return
		}
		dataLength := int64(len(data))
		s.task.Flow.Add(dataLength, 0)
		s.task.Client.Flow.Add(dataLength, dataLength)

		buf := common.BufPoolUdp.Get().([]byte)
		defer common.BufPoolUdp.Put(buf)

		for {
			clientConn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := target.Read(buf)
			if err != nil {
				s.addrMap.Delete(addr.String())
				logs.Warn(err)
				return
			}
			_, err = s.listener.WriteTo(buf[:n], addr)
			if err != nil {
				logs.Warn(err)
				return
			}

			n64 := int64(n)
			s.task.Flow.Add(0, n64)
			s.task.Client.Flow.Add(n64, n64)
		}
	}
}

func (s *UdpModeServer) Close() error {
	return s.listener.Close()
}
