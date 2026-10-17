package bridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
)

func (s *Bridge) verifyError(c *conn.Conn) {
	if !ServerSecureMode {
		_, _ = c.Write([]byte(common.VERIFY_EER))
	}
	_ = c.Close()
}

func (s *Bridge) verifySuccess(c *conn.Conn) {
	_, _ = c.Write([]byte(common.VERIFY_SUCCESS))
}

func (s *Bridge) CliProcess(c *conn.Conn, tunnelType string) {
	if c.Conn == nil || c.Conn.RemoteAddr() == nil {
		logs.Warn("Invalid connection")
		_ = c.Close()
		return
	}

	c.SetReadDeadlineBySecond(bridgeHandshakeReadTimeout)

	//read test flag
	if _, err := c.GetShortContent(3); err != nil {
		logs.Trace("The client %v connect error: %v", c.Conn.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	//version check
	minVerBytes, err := c.GetShortLenContent()
	if err != nil {
		logs.Trace("Failed to read version length from client %v: %v", c.Conn.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	ver := version.GetIndex(string(minVerBytes))
	if (ServerSecureMode && ver < version.MinVer) || ver == -1 {
		logs.Warn("Client %v basic version mismatch: expected %s, got %s", c.Conn.RemoteAddr(), version.GetLatest(), string(minVerBytes))
		_ = c.Close()
		return
	}

	//version get
	vs, err := c.GetShortLenContent()
	if err != nil {
		logs.Error("Failed to read client version from %v: %v", c.Conn.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	clientVer := string(bytes.TrimRight(vs, "\x00"))
	var id int

	if ver == 0 {
		// --- protocol 0.26.0 path ---
		//write server version to client
		if _, err := c.Write([]byte(crypt.Md5(version.GetVersion(ver)))); err != nil {
			logs.Error("Failed to write server version to client %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		//get vKey from client
		keyBuf, err := c.GetShortContent(32)
		if err != nil {
			logs.Trace("Failed to read vKey from client %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		//verify
		id, err = file.GetDb().GetIdByVerifyKey(string(keyBuf), c.RemoteAddr().String(), "", crypt.Md5)
		if err != nil {
			logs.Error("Validation error for client %v (proto-ver %d, vKey %x): %v", c.Conn.RemoteAddr(), ver, keyBuf, err)
			s.verifyError(c)
			return
		}
		s.verifySuccess(c)
	} else {
		// --- protocol 0.27.0+ path ---
		tsBuf, err := c.GetShortContent(8)
		if err != nil {
			logs.Error("Failed to read timestamp from client %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		ts := common.BytesToTimestamp(tsBuf)
		now := common.TimeNow().Unix()
		if ServerSecureMode && (ts > now+rep.ttl || ts < now-rep.ttl) {
			logs.Error("Timestamp validation failed for %v: ts=%d, now=%d", c.Conn.RemoteAddr(), ts, now)
			_ = c.Close()
			return
		}
		keyBuf, err := c.GetShortContent(64)
		if err != nil {
			logs.Error("Failed to read vKey (64 bytes) from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		//verify
		//id, err := file.GetDb().GetIdByVerifyKey(string(keyBuf), c.RateConn.RemoteAddr().String(), "", crypt.Blake2b)
		id, err = file.GetDb().GetClientIdByBlake2bVkey(string(keyBuf))
		if err != nil {
			logs.Error("Validation error for client %v (proto-ver %d, vKey %x): %v", c.Conn.RemoteAddr(), ver, keyBuf, err)
			s.verifyError(c)
			return
		}
		client, err := file.GetDb().GetClient(id)
		if err != nil {
			logs.Error("Failed to load client record for ID %d: %v", id, err)
			_ = c.Close()
			return
		}
		if !client.Status {
			logs.Info("Client %v (ID %d) is disabled", c.Conn.RemoteAddr(), id)
			_ = c.Close()
			return
		}
		remoteIP := common.GetIpByAddr(c.RemoteAddr().String())
		infoBuf, err := c.GetShortLenContent()
		if err != nil {
			logs.Error("Failed to read encrypted IP from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		infoDec, err := crypt.DecryptBytes(infoBuf, client.VerifyKey)
		if err != nil {
			logs.Error("Failed to decrypt Info for %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		if ver < 3 {
			// --- protocol 0.27.0 - 0.28.0 path ---
			client.Lock()
			client.Addr, client.LocalAddr, client.Mode = remoteIP, common.DecodeIP(infoDec).String(), tunnelType
			client.Unlock()
		} else {
			// --- protocol 0.29.0+ path ---
			// infoDec = [17-byte IP][1-byte L][L-byte tp]
			if len(infoDec) < 18 {
				logs.Error("Invalid payload length from %v: %d", c.Conn.RemoteAddr(), len(infoDec))
				_ = c.Close()
				return
			}
			ipPart := infoDec[:17]
			l := int(infoDec[17])
			if len(infoDec) < 18+l {
				logs.Error("Declared tp length %d exceeds payload from %v", l, c.Conn.RemoteAddr())
				_ = c.Close()
				return
			}
			ip := common.DecodeIP(ipPart)
			if ip == nil {
				logs.Error("Failed to decode IP from %v", c.Conn.RemoteAddr())
				_ = c.Close()
				return
			}
			tp := string(infoDec[18 : 18+l])
			// Several connections of one client may shake hands at once.
			client.Lock()
			client.Addr, client.LocalAddr, client.Mode = remoteIP, ip.String(), fmt.Sprintf("%s,%s", tunnelType, tp)
			client.Unlock()
		}
		randBuf, err := c.GetShortLenContent()
		if err != nil {
			logs.Error("Failed to read random buffer from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		hmacBuf, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("Failed to read HMAC from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		if ServerSecureMode && !bytes.Equal(hmacBuf, crypt.ComputeHMAC(client.VerifyKey, ts, minVerBytes, vs, infoBuf, randBuf)) {
			logs.Error("HMAC verification failed for %v", c.Conn.RemoteAddr())
			_ = c.Close()
			return
		}
		if ServerSecureMode && IsReplay(string(hmacBuf)) {
			logs.Error("Replay detected for client %v", c.Conn.RemoteAddr())
			_ = c.Close()
			return
		}
		if _, err := c.BufferWrite(crypt.ComputeHMAC(client.VerifyKey, ts, hmacBuf, []byte(version.GetVersion(ver)))); err != nil {
			logs.Error("Failed to write HMAC response to %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		if ver > 1 {
			// --- protocol 0.28.0+ path ---
			fpBuf, err := crypt.EncryptBytes(crypt.GetCertFingerprint(crypt.GetCert()), client.VerifyKey)
			if err != nil {
				logs.Error("Failed to encrypt cert fingerprint for %v: %v", c.Conn.RemoteAddr(), err)
				_ = c.Close()
				return
			}
			err = c.WriteLenContent(fpBuf)
			if err != nil {
				logs.Error("Failed to write cert fingerprint for %v: %v", c.Conn.RemoteAddr(), err)
				_ = c.Close()
				return
			}
			if ver > 3 {
				if ver > 5 {
					err := c.WriteLenContent([]byte(crypt.GetUUID().String()))
					if err != nil {
						logs.Error("Failed to write UUID for %v: %v", c.Conn.RemoteAddr(), err)
						_ = c.Close()
						return
					}
				}
				// --- protocol 0.30.0+ path ---
				randByte, err := common.RandomBytes(1000)
				if err != nil {
					logs.Error("Failed to generate rand byte for %v: %v", c.Conn.RemoteAddr(), err)
					_ = c.Close()
					return
				}
				if err := c.WriteLenContent(randByte); err != nil {
					logs.Error("Failed to write rand byte for %v: %v", c.Conn.RemoteAddr(), err)
					_ = c.Close()
					return
				}
			}
		}
		if err := c.FlushBuf(); err != nil {
			logs.Error("Failed to write to %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		//c.SetReadDeadlineBySecond(5)
	}
	go s.typeDeal(c, id, ver, clientVer, tunnelType, true)
	//return
}

func (s *Bridge) typeDeal(c *conn.Conn, id, ver int, vs, tunnelType string, first bool) {
	addr := c.RemoteAddr()
	flag, err := c.ReadFlag()
	if err != nil {
		logs.Warn("Failed to read operation flag from %v: %v", addr, err)
		_ = c.Close()
		return
	}
	var uuid string
	if ver > 3 {
		if ver > 5 {
			// --- protocol 0.30.0+ path ---
			uuidBuf, err := c.GetShortLenContent()
			if err != nil {
				logs.Error("Failed to read uuid buffer from %v: %v", addr, err)
				_ = c.Close()
				return
			}
			uuid = string(uuidBuf)
		}
		// --- protocol 0.30.0+ path ---
		_, err := c.GetShortLenContent()
		if err != nil {
			logs.Error("Failed to read random buffer from %v: %v", addr, err)
			_ = c.Close()
			return
		}
	}
	if uuid == "" {
		uuid = addr.String()
		if ver < 5 {
			uuid = common.GetIpByAddr(uuid)
		}
		uuid = crypt.GenerateUUID(uuid).String()
	}
	c.SetAlive()
	isPub := file.GetDb().IsPubClient(id)
	switch flag {
	case common.WORK_MAIN:
		if isPub {
			_ = c.Close()
			return
		}
		tcpConn, ok := c.Conn.(*net.TCPConn)
		if ok {
			// add tcp keep alive option for signal connection
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(5 * time.Second)
		}

		//the vKey connect by another, close the client of before
		node := NewNode(uuid, vs, ver)
		node.AddSignal(c)
		client := NewClient(id, node)
		if v, loaded := s.Client.LoadOrStore(id, client); loaded {
			client = v.(*Client)
			client.MarkConnectedNow()
			client.RemoveOfflineNodesExcept(uuid, true)
			n, ok := client.GetNodeByUUID(uuid)
			if ok {
				node = n
				node.AddSignal(c)
			} else {
				client.AddNode(node)
			}
		}
		client.MarkConnectedNow()
		go s.GetHealthFromClient(id, c, client, node)
		logs.Info("ClientId %d connection succeeded, address:%v ", id, addr)

	case common.WORK_CHAN:
		if !first {
			logs.Error("Can not create mux more than once")
			_ = c.Close()
			return
		}
		var anyConn any
		qc, ok := c.Conn.(*conn.QuicAutoCloseConn)
		if ok && ver > 4 {
			anyConn = qc.GetSession()
		} else {
			anyConn = mux.NewMux(c.Conn, tunnelType, s.disconnectTime, false)
		}
		if anyConn == nil {
			logs.Warn("Failed to create Mux for client %v", addr)
			_ = c.Close()
			return
		}
		node := NewNode(uuid, vs, ver)
		node.AddTunnel(anyConn)
		client := NewClient(id, node)
		if v, loaded := s.Client.LoadOrStore(id, client); loaded {
			client = v.(*Client)
			client.MarkConnectedNow()
			client.RemoveOfflineNodesExcept(uuid, true)
			n, ok := client.GetNodeByUUID(uuid)
			if ok {
				node = n
				node.AddTunnel(anyConn)
			} else {
				client.AddNode(node)
			}
		}
		client.MarkConnectedNow()
		if ver > 4 {
			go func() {
				defer func() {
					reason := tunnelCloseReason(anyConn)
					if reason != "" {
						logs.Trace("Tunnel connection closed, client %d, remote %v, reason: %s", id, addr, reason)
					} else {
						logs.Trace("Tunnel connection closed, client %d, remote %v", id, addr)
					}
					_ = c.Close()
					_ = node.Close()
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
				switch t := anyConn.(type) {
				case *mux.Mux:
					conn.Accept(t, func(c net.Conn) {
						mc, ok := c.(*mux.Conn)
						if ok {
							mc.SetPriority()
						}
						go s.typeDeal(conn.NewConn(c), id, ver, vs, tunnelType, false)
					})
					return
				case *quic.Conn:
					for {
						stream, err := t.AcceptStream(context.Background())
						if err != nil {
							logs.Trace("QUIC accept stream error: %v", err)
							return
						}
						sc := conn.NewQuicStreamConn(stream, t)
						go s.typeDeal(conn.NewConn(sc), id, ver, vs, tunnelType, false)
					}
				default:
					logs.Error("Unknown tunnel type")
				}
			}()
		}

	case common.WORK_CONFIG:
		client, err := file.GetDb().GetClient(id)
		if err != nil || (!isPub && !client.ConfigConnAllow) {
			_ = c.Close()
			return
		}
		_ = binary.Write(c, binary.LittleEndian, isPub)
		go s.getConfig(c, isPub, client, ver, vs, uuid)

	case common.WORK_REGISTER:
		go s.register(c)
		return

	case common.WORK_VISITOR:
		if !first {
			logs.Error("Can not create mux more than once")
			_ = c.Close()
			return
		}
		var anyConn any
		qc, ok := c.Conn.(*conn.QuicAutoCloseConn)
		if ok && ver > 4 {
			anyConn = qc.GetSession()
		} else {
			anyConn = mux.NewMux(c.Conn, tunnelType, s.disconnectTime, false)
		}
		if anyConn == nil {
			logs.Warn("Failed to create Mux for client %v", addr)
			_ = c.Close()
			return
		}
		go func() {
			idle := NewIdleTimer(30*time.Second, func() { _ = c.Close() })
			defer func() {
				logs.Trace("Visitor connection closed, client %d, remote %v", id, addr)
				idle.Stop()
				_ = c.Close()
			}()
			switch t := anyConn.(type) {
			case *mux.Mux:
				conn.Accept(t, func(nc net.Conn) {
					idle.Inc()
					go s.typeDeal(conn.NewConn(nc).OnClose(func(*conn.Conn) {
						idle.Dec()
					}), id, ver, vs, tunnelType, false)
				})
				return
			case *quic.Conn:
				for {
					stream, err := t.AcceptStream(context.Background())
					if err != nil {
						logs.Trace("QUIC accept stream error: %v", err)
						return
					}
					sc := conn.NewQuicStreamConn(stream, t)
					idle.Inc()
					go s.typeDeal(conn.NewConn(sc).OnClose(func(c *conn.Conn) {
						idle.Dec()
					}), id, ver, vs, tunnelType, false)
				}
			default:
				logs.Error("Unknown tunnel type")
			}
		}()

	case common.WORK_SECRET:
		b, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("secret error, failed to match the key successfully")
			_ = c.Close()
			return
		}
		s.SecretChan <- conn.NewSecret(string(b), c)

	case common.WORK_FILE:
		logs.Warn("clientId %d not support file", id)
		_ = c.Close()
		return
		//muxConn := mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime, false)
		//if v, loaded := s.Client.LoadOrStore(id, NewClient(id, c.RemoteAddr().String(), nil, muxConn, nil, ver, vs)); loaded {
		//	client := v.(*Client)
		//	//if client.file != nil {
		//	//	client.files.LoadOrStore(client.file, struct{}{})
		//	//}
		//	client.AddFile(muxConn)
		//}

	case common.WORK_P2P:
		s.handleP2PVisitor(c, id, ver, vs, uuid, tunnelType)
		return

	case common.WORK_P2P_SESSION:
		s.handleP2PJoin(c, id, ver, vs, tunnelType)
		return
	}

	c.SetAlive()
	//return
}

func tunnelCloseReason(v any) string {
	switch t := v.(type) {
	case *mux.Mux:
		return t.CloseReason()
	case *quic.Conn:
		if err := t.Context().Err(); err != nil {
			return err.Error()
		}
	}
	return ""
}

// register ip
func (s *Bridge) register(c *conn.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hour int32
	if err := binary.Read(c, binary.LittleEndian, &hour); err == nil {
		ip := common.GetIpByAddr(c.RemoteAddr().String())
		s.Register.Store(ip, time.Now().Add(time.Hour*time.Duration(hour)))
		logs.Info("Registered IP: %s for %d hours", ip, hour)
	} else {
		logs.Warn("Failed to register IP: %v", err)
	}
	_ = c.Close()
}
