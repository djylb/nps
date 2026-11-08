package proxy

import (
	"errors"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
)

type SecretServer struct {
	*BaseServer
	allowSecretLink  bool
	allowSecretLocal bool
}

func NewSecretServer(bridge NetBridge, task *model.Tunnel, allowLocalProxy, allowSecretLink, allowSecretLocal bool) *SecretServer {
	return &SecretServer{
		BaseServer:       NewBaseServer(bridge, task, allowLocalProxy),
		allowSecretLink:  allowSecretLink,
		allowSecretLocal: allowSecretLocal,
	}
}

func (s *SecretServer) HandleSecret(src net.Conn, wire conn.SecretWire) error {
	release, err := s.AcquireForwarding(s.Task.Client, s.Task.ControlGroup())
	if err != nil {
		_ = src.Close()
		return err
	}
	defer release()

	connType := conn.LinkTCP
	host := ""
	localProxy := false
	needAck := false

	var rb []byte
	tee := netx.NewRecordingConn(src, 0)
	// c wraps src only; it is closed on failure and CopyWaitGroup closes it otherwise.
	//noinspection GoResourceLeak
	c := conn.NewConn(tee)
	lk, err := wire.ReadLink(c)
	if err != nil || lk == nil {
		rb = tee.Buffered()
	}
	tee.Discard()

	if lk != nil {
		needAck = lk.Option.NeedAck
		if s.allowSecretLink {
			connType = lk.ConnType
			if lk.Host != "" {
				host = netaddr.FormatAddress(lk.Host)
				if s.Task.DestAclMode != model.AclOff && !s.Task.AllowsDestination(host) {
					destDenied(src.RemoteAddr(), s.Task, host)
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
			if s.Task.TargetType == model.TargetAll {
				switch lk.ConnType {
				case conn.LinkUDP:
					connType = conn.LinkUDP
				default:
					connType = conn.LinkTCP
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
		target, err = s.Bridge.Session(s.Task.Client.Id).OpenLink(link, s.Task.Id)
		return localProxy, err
	}
	if host == "" {
		err = route.Target.Dial(dial)
		if errors.Is(err, model.ErrNoTarget) {
			_, err = dial("")
		}
	} else {
		_, err = dial(host)
	}
	if err != nil {
		_ = c.Close()
		target := host
		if target == "" {
			target = route.Target.TargetStr
		}
		relayFailed(s.Task, target, err)
		return err
	}

	if needAck {
		if err := wire.WriteAck(c.Conn, link.Option.Timeout); err != nil {
			log.Debug("ack not written", "src_ip", srcIP(src.RemoteAddr()), "err", err)
			_ = c.Close()
			_ = target.Close()
			return err
		}
		trace("ack sent", "src_ip", srcIP(src.RemoteAddr()))
	}

	// The target is the backend side: the PROXY header and the bytes read
	// while probing for link information go to it. Encryption and
	// compression only apply to links through the client and are skipped
	// for local ones.
	proxyProtocol := route.Target.ProxyProtocol
	visitor := c.Conn
	if connType == conn.LinkUDP {
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
	start := time.Now()
	out, in := conn.CopyWaitGroup(target, visitor, link.Crypt, link.Compress, s.Task.Client.Rate, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Bridge.LinkCert(), proxyProtocol, rb, s.Task, localProxy, false)
	connClosed(s.Task, c.RemoteAddr(), link.Host, in, out, start)
	return nil
}
