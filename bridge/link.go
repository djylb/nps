package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/netaddr"
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

// Session returns the way to open links to client clientId.
func (s *Bridge) Session(clientId int) conn.ClientSession {
	return clientSession{bridge: s, id: clientId}
}

type clientSession struct {
	bridge *Bridge
	id     int
}

// OpenLink asks the client for a connection to link's target, or serves a
// local link itself. taskID is the task the link is for, 0 if none.
func (c clientSession) OpenLink(link *conn.Link, taskID int) (net.Conn, error) {
	return c.bridge.openLink(c.id, link, taskID)
}

func (s *Bridge) openLink(clientId int, link *conn.Link, taskID int) (target net.Conn, err error) {
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
		if link.ConnType == conn.LinkUDP5 {
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
				if taskID != 0 && taskID == id {
					return nil, fmt.Errorf("task %d cannot connect to itself (tunnel://%d)", taskID, id)
				}
				return s.TunnelConn(id, link.RemoteAddr)
			case "bridge":
				// bridge://tcp|tls|ws|wss|web|ipreg
				ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
				defer cancel()
				switch strings.ToLower(targetStr) {
				case conn.TransportTCP:
					return conn.DialVirtual(ctx, s.VirtualTcpListener, link.RemoteAddr)
				case conn.TransportTLS:
					return conn.DialVirtual(ctx, s.VirtualTlsListener, link.RemoteAddr)
				case conn.TransportWS:
					return conn.DialVirtual(ctx, s.VirtualWsListener, link.RemoteAddr)
				case conn.TransportWSS:
					return conn.DialVirtual(ctx, s.VirtualWssListener, link.RemoteAddr)
				case bridgeTargetWeb:
					return s.WebServerConn(ctx, link.RemoteAddr)
				case bridgeTargetIPReg:
					return s.IPRegisterConn(ctx, link.RemoteAddr)
				default:
					return nil, fmt.Errorf("invalid bridge target %q in %q", targetStr, raw)
				}
			default:
				return nil, fmt.Errorf("unsupported scheme %q in %q", scheme, raw)
			}
		}
		network := "tcp"
		if link.ConnType == conn.LinkUDP {
			network = "udp"
		}
		target, err = net.DialTimeout(network, netaddr.FormatAddress(link.Host), linkOpenTimeout(link))
		return
	}

	client := clientValue.(*Client)

	var tunnel legacy.Carrier
	var node *Node
	isFile := strings.Contains(link.Host, "file://")
	if isFile {
		link.ConnType = "file"
	}
	if link.Option.Node != "" {
		// The route chose the node; its absence says nothing about the others.
		if node, ok = client.GetNodeByUUID(link.Option.Node); ok {
			tunnel = node.GetTunnel()
		}
		if tunnel == nil {
			return nil, fmt.Errorf("node %s of client %d is not connected", link.Option.Node, clientId)
		}
	} else if isFile {
		key := strings.TrimPrefix(strings.TrimSpace(link.Host), "file://")
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
		if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
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
			if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
				err = errors.New("client is connecting, please retry")
				return
			}
			s.DelClient(clientId)
			err = errors.New("the client is offline")
			return
		}
		if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
			err = errors.New("client tunnel is reconnecting, please retry")
			return
		}
		s.DelClient(clientId)
		err = errors.New("the client tunnel is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
	target, err = tunnel.OpenStream(ctx)
	cancel()
	if err != nil {
		return
	}

	if err = legacy.WriteLink(target, link); err != nil {
		logs.Info("new connection error, the target %s refused to connect", link.Host)
		_ = target.Close()
		return
	}

	caps := node.Caps()
	if link.Option.NeedAck && caps.LinkAck {
		if err := legacy.ReadAck(target, link.Option.Timeout); err != nil {
			_ = target.Close()
			logs.Trace("ReadACK failed: %v", err)
			node.CloseIfTunnelCurrent(tunnel)
			return nil, err
		}
	}

	if link.ConnType == conn.LinkUDP && !caps.FramedUDP {
		logs.Warn("UDP connection needs a newer client than protocol %s.", legacy.CoreVersion(caps.Index))
	}

	return
}

// Local targets of bridge:// addresses besides the bridge transports.
const (
	bridgeTargetWeb   = "web"
	bridgeTargetIPReg = "ipreg"
)
