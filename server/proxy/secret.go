package proxy

import (
	"net"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type SecretServer struct {
	*BaseServer
	allowSecretLink  bool
	allowSecretLocal bool
}

func NewSecretServer(bridge NetBridge, task *file.Tunnel, allowLocalProxy, allowSecretLink, allowSecretLocal bool) *SecretServer {
	return &SecretServer{
		BaseServer:       NewBaseServer(bridge, task, allowLocalProxy),
		allowSecretLink:  allowSecretLink,
		allowSecretLocal: allowSecretLocal,
	}
}

func (s *SecretServer) HandleSecret(src net.Conn) error {
	s.Task.AddConn()
	defer s.Task.CutConn()
	if err := s.CheckFlowAndConnNum(s.Task.Client); err != nil {
		logs.Warn("Connection limit exceeded, client id %d, host id %d, error %v", s.Task.Client.Id, s.Task.Id, err)
		return err
	}
	defer s.Task.Client.CutConn()

	connType := common.CONN_TCP
	host, _ := s.Task.Target.GetRandomTarget()
	localProxy := false
	needAck := false

	var rb []byte
	tee := conn.NewTeeConn(src)
	c := conn.NewConn(tee)
	lk, err := c.GetLinkInfo()
	if err != nil || lk == nil {
		rb = tee.Buffered()
	}
	tee.StopAndClean()

	if lk != nil {
		needAck = lk.Option.NeedAck
		if s.allowSecretLink {
			connType = lk.ConnType
			if lk.Host != "" {
				host = common.FormatAddress(lk.Host)
			}
			if s.allowSecretLocal {
				localProxy = lk.LocalProxy
			} else {
				localProxy = s.Task.Target.LocalProxy
			}
		} else {
			if s.Task.TargetType == common.CONN_ALL {
				switch lk.ConnType {
				case common.CONN_UDP:
					connType = common.CONN_UDP
				default:
					connType = common.CONN_TCP
				}
			} else {
				connType = s.Task.TargetType
			}
		}
	}
	localProxy = s.AllowLocalProxy && localProxy || s.Task.Client.Id < 0
	link := conn.NewLink(connType, host, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, c.Conn.RemoteAddr().String(), localProxy)
	target, err := s.Bridge.SendLinkInfo(s.Task.Client.Id, link, s.Task)
	if err != nil {
		_ = c.Close()
		logs.Warn("failed to get backend connection: %v", err)
		return err
	}

	if needAck {
		if err := conn.WriteACK(c.Conn, link.Option.Timeout); err != nil {
			logs.Warn("write ACK failed: %v", err)
			_ = c.Close()
			_ = target.Close()
			return err
		}
		logs.Trace("sent ACK before proceeding")
	}

	if localProxy {
		isFramed := connType == common.CONN_UDP
		conn.CopyWaitGroup(c.Conn, target, false, false, s.Task.Client.Rate, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, false, s.Task.Target.ProxyProtocol, rb, s.Task, localProxy, isFramed)
	} else {
		conn.CopyWaitGroup(target, c.Conn, link.Crypt, link.Compress, s.Task.Client.Rate, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, true, s.Task.Target.ProxyProtocol, rb, s.Task, localProxy, false)
	}
	return nil
}
