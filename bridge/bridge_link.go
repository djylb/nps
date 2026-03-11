package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/tool"
	"github.com/quic-go/quic-go"
)

const clientConnectGraceWindow = 3 * time.Second

func (s *Bridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error) {
	if link == nil {
		return nil, errors.New("link is nil")
	}

	// If IP is restricted, do IP verification
	if s.ipVerify {
		ip := common.GetIpByAddr(link.RemoteAddr)
		ipValue, ok := s.Register.Load(ip)
		if !ok {
			return nil, fmt.Errorf("the ip %s is not in the validation list", ip)
		}

		if !ipValue.(time.Time).After(time.Now()) {
			return nil, fmt.Errorf("the validity of the ip %s has expired", ip)
		}
	}

	clientValue, ok := s.Client.Load(clientId)
	if !ok {
		err = fmt.Errorf("the client %d is not connect", clientId)
		return
	}

	// if the proxy type is local
	if link.LocalProxy || clientId < 0 {
		if link.ConnType == "udp5" {
			serverSide, handlerSide := net.Pipe()
			go conn.HandleUdp5(context.Background(), handlerSide, link.Option.Timeout, "")
			return serverSide, nil
		}
		raw := strings.TrimSpace(link.Host)
		if raw == "" {
			return nil, fmt.Errorf("empty host")
		}
		if idx := strings.Index(raw, "://"); idx >= 0 {
			scheme := strings.ToLower(strings.TrimSpace(raw[:idx]))
			targetStr := strings.TrimSpace(raw[idx+3:])
			if targetStr == "" {
				return nil, fmt.Errorf("missing target in %q", raw)
			}
			switch scheme {
			case "tunnel":
				id, convErr := strconv.Atoi(targetStr)
				if convErr != nil {
					return nil, fmt.Errorf("invalid tunnel id %q: %w", targetStr, convErr)
				}
				if t != nil && t.Id == id {
					return nil, fmt.Errorf("task %d cannot connect to itself (tunnel://%d)", t.Id, id)
				}
				return tool.GetTunnelConn(id, link.RemoteAddr)
			case "bridge":
				// bridge://tcp|tls|ws|wss|web
				switch strings.ToLower(targetStr) {
				case common.CONN_TCP:
					return s.VirtualTcpListener.DialVirtual(link.RemoteAddr)
				case common.CONN_TLS:
					return s.VirtualTlsListener.DialVirtual(link.RemoteAddr)
				case common.CONN_WS:
					return s.VirtualWsListener.DialVirtual(link.RemoteAddr)
				case common.CONN_WSS:
					return s.VirtualWssListener.DialVirtual(link.RemoteAddr)
				case common.CONN_WEB:
					return tool.GetWebServerConn(link.RemoteAddr)
				default:
					return nil, fmt.Errorf("invalid bridge target %q in %q", targetStr, raw)
				}
			default:
				return nil, fmt.Errorf("unsupported scheme %q in %q", scheme, raw)
			}
		}
		network := "tcp"
		if link.ConnType == common.CONN_UDP {
			network = "udp"
		}
		target, err = net.Dial(network, common.FormatAddress(link.Host))
		return
	}

	client := clientValue.(*Client)

	var tunnel any
	var node *Node
	if strings.Contains(link.Host, "file://") {
		key := strings.TrimPrefix(strings.TrimSpace(link.Host), "file://")
		link.ConnType = "file"
		node, ok = client.GetNodeByFile(key)
		if ok {
			tunnel = node.GetTunnel()
		} else {
			logs.Warn("Failed to find tunnel for host: %s", link.Host)
			err = fmt.Errorf("failed to find tunnel for host: %s", link.Host)
			client.RemoveOfflineNodes(false)
			return
		}
	} else {
		node = client.GetNode()
		if node != nil {
			tunnel = node.GetTunnel()
		}
	}

	if node == nil {
		if client.InConnectGraceWindow(clientConnectGraceWindow) {
			err = errors.New("client is connecting, please retry")
			return
		}
		s.DelClient(clientId)
		err = errors.New("the client connect error")
		return
	}
	if tunnel == nil {
		sig := node.GetSignal()
		if sig == nil || sig.IsClosed() {
			if client.InConnectGraceWindow(clientConnectGraceWindow) {
				err = errors.New("client is connecting, please retry")
				return
			}
			s.DelClient(clientId)
			err = errors.New("the client is offline")
			return
		}
		if client.InConnectGraceWindow(clientConnectGraceWindow) {
			err = errors.New("client tunnel is reconnecting, please retry")
			return
		}
		s.DelClient(clientId)
		err = errors.New("the client tunnel is unavailable")
		return
	}

	switch tun := tunnel.(type) {
	case *mux.Mux:
		target, err = tun.NewConn()
	case *quic.Conn:
		var stream *quic.Stream
		stream, err = tun.OpenStreamSync(context.Background())
		if err == nil {
			target = conn.NewQuicStreamConn(stream, tun)
		}
	default:
		err = errors.New("the tunnel type error")
		return
	}

	if err != nil {
		return
	}

	if _, err = conn.NewConn(target).SendInfo(link, ""); err != nil {
		logs.Info("new connection error, the target %s refused to connect", link.Host)
		_ = target.Close()
		return
	}

	if link.Option.NeedAck && node.BaseVer > 5 {
		if err := conn.ReadACK(target, link.Option.Timeout); err != nil {
			_ = target.Close()
			logs.Trace("ReadACK failed: %v", err)
			_ = node.Close()
			return nil, err
		}
	}

	if link.ConnType == "udp" && node.BaseVer < 7 {
		logs.Warn("UDP connection requires client v%s or newer.", version.GetVersion(7))
	}

	return
}
