package bridge

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
)

// allowsFlag is the one rule for which workflows each kind of vkey may open.
func allowsFlag(kind model.KeyKind, flag string) bool {
	switch flag {
	case legacy.WorkRegister, legacy.WorkConfig:
		return kind != model.KeyVisitor
	case legacy.WorkVisitor, legacy.WorkSecret, legacy.WorkP2P:
		return true
	case legacy.WorkMain:
		return kind == model.KeyControl
	case legacy.WorkChan, legacy.WorkFile, legacy.WorkP2PSession:
		return kind == model.KeyControl || kind == model.KeyPublic
	}
	return false
}

// NewServerUUID makes the server UUID a handshake sends; tests that record
// the handshake fix it.
var NewServerUUID = func() string { return crypt.GetUUID().String() }

func (s *Bridge) CliProcess(c *conn.Conn, tunnelType string) {
	if c.Conn == nil || c.Conn.RemoteAddr() == nil {
		logs.Warn("Invalid connection")
		_ = c.Close()
		return
	}
	remote := c.Conn.RemoteAddr()
	if file.GetDb().GlobalDenied(remote, "Bridge") {
		_ = c.Close()
		return
	}

	c.SetReadDeadlineBySecond(bridgeHandshakeReadTimeout)
	window := servercfg.Current().Security.AuthTimeWindow
	res, err := legacy.ServerHandshake(c, remote, legacy.ServerAuth{
		Secure: ServerSecureMode,
		LookupDigest: func(digest string) (legacy.Credential, error) {
			id, err := file.GetDb().GetClientIdByBlake2bVkey(digest)
			if err != nil {
				return legacy.Credential{}, err
			}
			client, err := file.GetDb().GetClient(id)
			if err != nil {
				return legacy.Credential{}, err
			}
			return legacy.Credential{ID: id, Key: client.VerifyKey, Enabled: client.Status}, nil
		},
		LookupMD5: func(digest string, remote net.Addr) (legacy.Credential, error) {
			id, err := file.GetDb().GetIdByVerifyKey(digest, remote.String(), "", crypt.Md5)
			if err != nil {
				return legacy.Credential{}, err
			}
			return legacy.Credential{ID: id, Enabled: true}, nil
		},
		CheckTime:   func(ts int64) error { return authReplays.checkTime(ts, window) },
		CheckReplay: func(ts int64, mac []byte) error { return authReplays.check(ts, mac, window) },
		Fingerprint: func() []byte { return crypt.GetCertFingerprint(crypt.GetCert()) },
		NewUUID:     NewServerUUID,
	})
	if err != nil {
		switch {
		case errors.Is(err, legacy.ErrProbe):
			logs.Trace("The client %v connect error: %v", remote, err)
		case errors.Is(err, legacy.ErrVersion):
			logs.Warn("Client %v: %v", remote, err)
		case errors.Is(err, legacy.ErrDisabled):
			logs.Info("Client %v: %v", remote, err)
		default:
			logs.Error("Handshake with client %v failed: %v", remote, err)
		}
		_ = c.Close()
		return
	}
	if res.Caps.TimestampAuth {
		if client, err := file.GetDb().GetClient(res.ClientID); err == nil {
			mode := tunnelType
			if res.Caps.TransportInInfo {
				mode = fmt.Sprintf("%s,%s", tunnelType, res.Transport)
			}
			// Several connections of one client may shake hands at once.
			client.Lock()
			client.Addr, client.LocalAddr, client.Mode = common.GetIpByAddr(c.RemoteAddr().String()), res.LocalIP.String(), mode
			client.Unlock()
		}
	}
	go s.typeDeal(c, res.ClientID, res.Caps, tunnelType, true)
}

func (s *Bridge) typeDeal(c *conn.Conn, id int, caps legacy.Caps, tunnelType string, first bool) {
	addr := c.RemoteAddr()
	flag, uuid, err := legacy.ReadWork(c, caps, addr.String())
	if err != nil {
		logs.Warn("Failed to read the work request from %v: %v", addr, err)
		_ = c.Close()
		return
	}
	c.SetAlive()
	keyClient, err := file.GetDb().GetClient(id)
	if err != nil || !allowsFlag(keyClient.KeyKind, flag) {
		logs.Warn("Workflow %q of client %d from %v refused", flag, id, addr)
		_ = c.Close()
		return
	}
	isPub := file.GetDb().IsPubClient(id)
	switch flag {
	case legacy.WorkMain:
		tcpConn, ok := c.Conn.(*net.TCPConn)
		if ok {
			// add tcp keep alive option for signal connection
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(5 * time.Second)
		}

		//the vKey connect by another, close the client of before
		node := newNode(uuid, caps)
		node.AddSignal(c)
		client, node := s.joinNode(id, uuid, node, func(n *Node) { n.AddSignal(c) })
		go s.GetHealthFromClient(id, c, client, node)
		logs.Info("ClientId %d connection succeeded, address:%v ", id, addr)

	case legacy.WorkChan:
		if !first {
			logs.Error("Can not create mux more than once")
			_ = c.Close()
			return
		}
		tunnel := legacy.NewTunnel(c, tunnelType, caps, s.disconnectTime, false)
		node := newNode(uuid, caps)
		node.AddTunnel(tunnel)
		client, node := s.joinNode(id, uuid, node, func(n *Node) { n.AddTunnel(tunnel) })
		if caps.TunnelStreams {
			go func() {
				defer func() {
					reason := tunnel.Reason()
					if reason != "" {
						logs.Trace("Tunnel connection closed, client %d, remote %v, reason: %s", id, addr, reason)
					} else {
						logs.Trace("Tunnel connection closed, client %d, remote %v", id, addr)
					}
					_ = c.Close()
					node.CloseIfTunnelCurrent(tunnel)
					removed := client.RemoveOfflineNodes(false)
					logs.Warn(
						"Disconnect summary event=disconnect_summary role=server client=%d uuid=%s remote=%v removed=%d remaining=%d reason=%q",
						id,
						uuid,
						addr,
						removed,
						client.NodeCount(),
						reason,
					)
				}()
				serveStreams(tunnel, func(stream net.Conn) {
					if p, ok := stream.(interface{ SetPriority() }); ok {
						p.SetPriority()
					}
					go s.typeDeal(conn.NewConn(stream), id, caps, tunnelType, false)
				})
			}()
		}

	case legacy.WorkConfig:
		client, err := file.GetDb().GetClient(id)
		if err != nil || (!isPub && !client.ConfigConnAllow) {
			_ = c.Close()
			return
		}
		_ = binary.Write(c, binary.LittleEndian, isPub)
		go s.getConfig(c, isPub, client, caps, uuid)

	case legacy.WorkRegister:
		go s.register(c, id)
		return

	case legacy.WorkVisitor:
		if !first {
			logs.Error("Can not create mux more than once")
			_ = c.Close()
			return
		}
		tunnel := legacy.NewTunnel(c, tunnelType, caps, s.disconnectTime, false)
		go func() {
			idle := NewIdleTimer(30*time.Second, func() { _ = c.Close() })
			defer func() {
				logs.Trace("Visitor connection closed, client %d, remote %v", id, addr)
				idle.Stop()
				_ = c.Close()
			}()
			serveStreams(tunnel, func(stream net.Conn) {
				idle.Inc()
				go s.typeDeal(conn.NewConn(stream).OnClose(func(*conn.Conn) {
					idle.Dec()
				}), id, caps, tunnelType, false)
			})
		}()

	case legacy.WorkSecret:
		c.SetReadDeadlineBySecond(bridgeHandshakeReadTimeout)
		b, err := legacy.ReadExact(c, 32)
		if err != nil {
			logs.Error("secret error, failed to match the key successfully")
			_ = c.Close()
			return
		}
		c.SetAlive()
		if !dispatch(s.SecretChan, conn.NewSecret(string(b), c, legacy.SecretWire)) {
			logs.Warn("secret connection from %v dropped: server busy", addr)
			_ = c.Close()
			return
		}

	case legacy.WorkFile:
		logs.Warn("clientId %d not support file", id)
		_ = c.Close()
		return
		//muxConn := mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime, false)
		//if v, loaded := s.loadOrStoreClient(id, NewClient(id, c.RemoteAddr().String(), nil, muxConn, nil, ver, vs)); loaded {
		//	client := v.(*Client)
		//	//if client.file != nil {
		//	//	client.files.LoadOrStore(client.file, struct{}{})
		//	//}
		//	client.AddFile(muxConn)
		//}

	case legacy.WorkP2P:
		s.handleP2PVisitor(c, id, caps, uuid, tunnelType)
		return

	case legacy.WorkP2PSession:
		s.handleP2PJoin(c, id, caps, tunnelType)
		return
	}

	c.SetAlive()
	//return
}

// serveStreams hands each stream the peer opens on tunnel to handle until the
// tunnel ends.
func serveStreams(tunnel legacy.Carrier, handle func(net.Conn)) {
	for {
		stream, err := tunnel.AcceptStream(context.Background())
		if err != nil {
			logs.Trace("Tunnel accept stream error: %v", err)
			return
		}
		handle(stream)
	}
}

// register registers the address of the npc on c for its client, or for
// every client when it used the public key, for the hours npc asks.
func (s *Bridge) register(c *conn.Conn, id int) {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hour int32
	if err := binary.Read(c, binary.LittleEndian, &hour); err == nil {
		if ip, ok := ipreg.ParseIP(c.RemoteAddr().String()); ok && hour > 0 {
			// the public key is an administrator's key: it registers for
			// every client
			if file.GetDb().IsPubClient(id) {
				id = ipreg.AnyClient
			}
			until := ipreg.Register(id, ip, time.Hour*time.Duration(hour))
			logs.Info("Registered IP %s for client %d until %s", ip, id, until.Format("2006-01-02 15:04:05"))
		}
	} else {
		logs.Warn("Failed to register IP: %v", err)
	}
	_ = c.Close()
}

// MinClientVersion is the oldest npc core version the bridge accepts.
func MinClientVersion() string { return legacy.MinCoreVersion(ServerSecureMode) }

// NewLocalNode returns the node of the client that nps runs itself, which
// speaks the latest protocol.
func NewLocalNode(uuid, version string) *Node { return NewNode(uuid, version, legacy.LatestIndex) }
