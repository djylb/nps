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
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
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
		if before, after, ok := strings.Cut(raw, "://"); ok {
			scheme := strings.ToLower(strings.TrimSpace(before))
			targetStr := strings.TrimSpace(after)
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

	var path linkPath
	var node *Node
	isFile := strings.Contains(link.Host, "file://")
	if isFile {
		link.ConnType = "file"
	}
	if link.Option.Node != "" {
		// The route chose the node; its absence says nothing about the others.
		if node, ok = client.GetNodeByUUID(link.Option.Node); ok {
			path = node.path()
		}
		if path == nil {
			return nil, fmt.Errorf("node %s of client %d is not connected", link.Option.Node, clientId)
		}
	} else if isFile {
		key := strings.TrimPrefix(strings.TrimSpace(link.Host), "file://")
		node, ok = client.GetNodeByFile(key)
		if ok {
			path = node.path()
		} else {
			log.Debug("no node holds the file tunnel", "client", clientId)
			err = fmt.Errorf("failed to find tunnel for host: %s", link.Host)
			client.RemoveOfflineNodes(false)
			return
		}
	} else {
		node = client.GetNode()
		if node != nil {
			path = node.path()
		}
	}

	if node == nil {
		if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
			err = errors.New("client is connecting, please retry")
			return
		}
		s.delClient(clientId, "closed")
		err = errors.New("the client connect error")
		return
	}
	if path == nil {
		sig := node.GetSignal()
		if sig == nil || sig.IsClosed() {
			if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
				err = errors.New("client is connecting, please retry")
				return
			}
			s.delClient(clientId, "closed")
			err = errors.New("the client is offline")
			return
		}
		if client.InConnectGraceWindow(clientConnectGraceWindow) || client.awaitingTunnel() {
			err = errors.New("client tunnel is reconnecting, please retry")
			return
		}
		s.delClient(clientId, "closed")
		err = errors.New("the client tunnel is unavailable")
		return
	}

	return path.open(clientId, link)
}

// linkPath is how links reach one node, whichever protocol it speaks.
type linkPath interface {
	open(clientId int, link *conn.Link) (net.Conn, error)
}

// legacyPath opens links on a carrier of the old protocol.
type legacyPath struct {
	node   *Node
	tunnel legacy.Carrier
}

func (p legacyPath) open(clientId int, link *conn.Link) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
	target, err := p.tunnel.OpenStream(ctx)
	cancel()
	if err != nil {
		return nil, err
	}

	if err = legacy.WriteLink(target, link); err != nil {
		log.Debug("writing link failed", "client", clientId, "target", link.Host, "err", err)
		_ = target.Close()
		return nil, err
	}

	caps := p.node.Caps()
	if link.Option.NeedAck && caps.LinkAck {
		if err := legacy.ReadAck(target, link.Option.Timeout); err != nil {
			_ = target.Close()
			log.Debug("reading link ack failed", "client", clientId, "target", link.Host, "err", err)
			p.node.CloseIfTunnelCurrent(p.tunnel)
			return nil, err
		}
	}

	if link.ConnType == conn.LinkUDP && !caps.FramedUDP && p.node.udpWarned.CompareAndSwap(false, true) {
		log.Info("udp link needs a newer npc", "client", clientId, "version", legacy.CoreVersion(caps.Index))
	}
	return target, nil
}

// corePath opens links on a core protocol session, which encrypts and
// compresses them itself.
type corePath struct{ s coreapi.Session }

func (p corePath) open(clientId int, link *conn.Link) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), linkOpenTimeout(link))
	defer cancel()
	req := coreapi.LinkRequest{Source: link.RemoteAddr, WantReply: link.Option.NeedAck, Compress: link.Option.Compress}
	switch link.ConnType {
	case conn.LinkPing:
		if _, err := p.s.Ping(ctx); err != nil {
			return nil, err
		}
		return closedConn(), nil
	case conn.LinkTCP:
		req.Network, req.Target = coreapi.NetworkTCP, netaddr.FormatAddress(link.Host)
	case conn.LinkUDP:
		req.Network, req.Target = coreapi.NetworkUDP, netaddr.FormatAddress(link.Host)
	case conn.LinkUDP5:
		req.Network = coreapi.NetworkUDP
	default:
		return nil, fmt.Errorf("the client %d cannot serve a %s link", clientId, link.ConnType)
	}
	target, err := p.s.OpenLink(ctx, req)
	if err != nil {
		log.Debug("opening link failed", "client", clientId, "target", link.Host, "err", err)
		return nil, err
	}
	link.Crypt, link.Compress = false, false
	return target, nil
}

// closedConn is the connection of a link that carries nothing.
func closedConn() net.Conn {
	c, peer := net.Pipe()
	_ = peer.Close()
	return c
}

// Local targets of bridge:// addresses besides the bridge transports.
const (
	bridgeTargetWeb   = "web"
	bridgeTargetIPReg = "ipreg"
)
