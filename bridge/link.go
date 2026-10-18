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

// defaultLinkOpenTimeout bounds opening a link when the link carries no timeout.
const defaultLinkOpenTimeout = 5 * time.Second

func linkOpenTimeout(link *conn.Link) time.Duration {
	if link.Option.Timeout > 0 {
		return link.Option.Timeout
	}
	return defaultLinkOpenTimeout
}

func (s *Bridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error) {
	if link == nil {
		return nil, errors.New("link is nil")
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
				// bridge://tcp|tls|ws|wss|web|ipreg
				ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
				defer cancel()
				switch strings.ToLower(targetStr) {
				case common.ConnTCP:
					return conn.DialVirtual(ctx, s.VirtualTcpListener, link.RemoteAddr)
				case common.ConnTLS:
					return conn.DialVirtual(ctx, s.VirtualTlsListener, link.RemoteAddr)
				case common.ConnWS:
					return conn.DialVirtual(ctx, s.VirtualWsListener, link.RemoteAddr)
				case common.ConnWSS:
					return conn.DialVirtual(ctx, s.VirtualWssListener, link.RemoteAddr)
				case common.ConnWeb:
					return tool.GetWebServerConn(ctx, link.RemoteAddr)
				case "ipreg":
					return tool.GetIPRegisterConn(ctx, link.RemoteAddr)
				default:
					return nil, fmt.Errorf("invalid bridge target %q in %q", targetStr, raw)
				}
			default:
				return nil, fmt.Errorf("unsupported scheme %q in %q", scheme, raw)
			}
		}
		network := "tcp"
		if link.ConnType == common.ConnUDP {
			network = "udp"
		}
		target, err = net.DialTimeout(network, common.FormatAddress(link.Host), linkOpenTimeout(link))
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
		ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
		stream, err = tun.OpenStreamSync(ctx)
		cancel()
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
			node.CloseIfTunnelCurrent(tunnel)
			return nil, err
		}
	}

	if link.ConnType == "udp" && node.BaseVer < 7 {
		logs.Warn("UDP connection requires client v%s or newer.", version.GetVersion(7))
	}

	return
}
