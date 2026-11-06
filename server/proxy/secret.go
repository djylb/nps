package proxy

import (
	"errors"
	"net"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
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
	release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
	if err != nil {
		_ = src.Close()
		return err
	}
	defer release()

	connType := common.ConnTCP
	host := ""
	localProxy := false
	needAck := false

	var rb []byte
	tee := netx.NewRecordingConn(src, 0)
	// c wraps src only; it is closed on failure and CopyWaitGroup closes it otherwise.
	//noinspection GoResourceLeak
	c := conn.NewConn(tee)
	lk, err := legacy.ReadLink(c)
	if err != nil || lk == nil {
		rb = tee.Buffered()
	}
	tee.Discard()

	if lk != nil {
		needAck = lk.Option.NeedAck
		if s.allowSecretLink {
			connType = lk.ConnType
			if lk.Host != "" {
				host = common.FormatAddress(lk.Host)
				if s.Task.DestAclMode != file.AclOff && !s.Task.AllowsDestination(host) {
					logs.Warn("secret dest acl deny: client=%d task=%d dest=%s", s.Task.Client.Id, s.Task.Id, common.ExtractHost(host))
					_ = c.Close()
					return errProxyAccessDenied
				}
			}
			if s.allowSecretLocal {
				localProxy = lk.LocalProxy
			} else {
				localProxy = s.Task.Target.LocalProxy
			}
		} else {
			if s.Task.TargetType == common.ConnAll {
				switch lk.ConnType {
				case common.ConnUDP:
					connType = common.ConnUDP
				default:
					connType = common.ConnTCP
				}
			} else {
				connType = s.Task.TargetType
			}
		}
	}
	localProxy = s.AllowLocalProxy && localProxy || s.Task.Client.Id < 0
	route := s.Task.Route()
	var link *conn.Link
	var target net.Conn
	dial := func(addr string) (bool, error) {
		link = conn.NewLink(connType, addr, s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, c.Conn.RemoteAddr().String(), localProxy)
		link.Option.Node = route.Node
		var err error
		target, err = s.Bridge.SendLinkInfo(s.Task.Client.Id, link, s.Task)
		return localProxy, err
	}
	if host == "" {
		err = route.Target.Dial(dial)
		if errors.Is(err, file.ErrNoTarget) {
			_, err = dial("")
		}
	} else {
		_, err = dial(host)
	}
	if err != nil {
		_ = c.Close()
		logs.Warn("failed to get backend connection: %v", err)
		return err
	}

	if needAck {
		if err := legacy.WriteAck(c.Conn, link.Option.Timeout); err != nil {
			logs.Warn("write ACK failed: %v", err)
			_ = c.Close()
			_ = target.Close()
			return err
		}
		logs.Trace("sent ACK before proceeding")
	}

	// The target is the backend side: the PROXY header and the bytes read
	// while probing for link information go to it. Encryption and
	// compression only apply to links through the client and are skipped
	// for local ones.
	proxyProtocol := route.Target.ProxyProtocol
	visitor := c.Conn
	if connType == common.ConnUDP {
		// The visitor stream carries length-framed datagrams, where a single
		// stream-level PROXY header would be taken for a frame.
		proxyProtocol = 0
		if localProxy {
			// The target is a UDP socket: unframe on the visitor side.
			// CopyWaitGroup closes visitor.
			//noinspection GoResourceLeak
			visitor = netx.NewFramedConn(c.Conn, netx.WithFramedDatagrams())
		}
	}
	conn.CopyWaitGroup(target, visitor, link.Crypt, link.Compress, s.Task.Client.Rate, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, true, proxyProtocol, rb, s.Task, localProxy, false)
	return nil
}
