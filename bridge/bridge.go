package bridge

import (
	"bytes"
	"crypto/tls"
	_ "crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/nps_mux"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/tool"
)

var (
	ServerTcpEnable  = false
	ServerKcpEnable  = false
	ServerQuicEnable = false
	ServerTlsEnable  = false
	ServerWsEnable   = false
	ServerWssEnable  = false
	ServerSecureMode = false
)

type Bridge struct {
	TunnelPort     int
	Client         *sync.Map
	Register       *sync.Map
	tunnelType     string //bridge type kcp or tcp
	OpenTask       chan *file.Tunnel
	CloseTask      chan *file.Tunnel
	CloseClient    chan int
	SecretChan     chan *conn.Secret
	ipVerify       bool
	runList        *sync.Map //map[int]interface{}
	disconnectTime int
}

func NewTunnel(tunnelPort int, tunnelType string, ipVerify bool, runList *sync.Map, disconnectTime int) *Bridge {
	return &Bridge{
		TunnelPort:     tunnelPort,
		tunnelType:     tunnelType,
		Client:         &sync.Map{},
		Register:       &sync.Map{},
		OpenTask:       make(chan *file.Tunnel, 100),
		CloseTask:      make(chan *file.Tunnel, 100),
		CloseClient:    make(chan int, 100),
		SecretChan:     make(chan *conn.Secret, 100),
		ipVerify:       ipVerify,
		runList:        runList,
		disconnectTime: disconnectTime,
	}
}

func (s *Bridge) StartTunnel() error {
	go s.ping()
	//if s.tunnelType == "kcp" {
	//	logs.Info("server start, the bridge type is %s, the bridge port is %d", s.tunnelType, s.TunnelPort)
	//	return conn.NewKcpListenerAndProcess(common.BuildAddress(beego.AppConfig.String("bridge_ip"), beego.AppConfig.String("bridge_port")), func(c net.RateConn) {
	//		s.cliProcess(conn.NewConn(c), "kcp")
	//	})
	//}

	// tcp
	if ServerTcpEnable {
		go func() {
			listener, err := connection.GetBridgeTcpListener()
			if err != nil {
				logs.Error("%v", err)
				os.Exit(0)
				return
			}
			conn.Accept(listener, func(c net.Conn) {
				s.cliProcess(conn.NewConn(c), "tcp")
			})
		}()
	}

	// tls
	if ServerTlsEnable {
		go func() {
			tlsListener, tlsErr := connection.GetBridgeTlsListener()
			if tlsErr != nil {
				logs.Error("%v", tlsErr)
				os.Exit(0)
				return
			}
			conn.Accept(tlsListener, func(c net.Conn) {
				s.cliProcess(conn.NewConn(tls.Server(c, &tls.Config{Certificates: []tls.Certificate{crypt.GetCert()}})), "tls")
			})
		}()
	}

	// ws
	if ServerWsEnable {
		go func() {
			wsListener, wsErr := connection.GetBridgeWsListener()
			if wsErr != nil {
				logs.Error("%v", wsErr)
				os.Exit(0)
				return
			}
			wsLn := conn.NewWSListener(wsListener, beego.AppConfig.String("bridge_path"))
			conn.Accept(wsLn, func(c net.Conn) {
				s.cliProcess(conn.NewConn(c), "ws")
			})
		}()
	}

	// wss
	if ServerWssEnable {
		go func() {
			wssListener, wssErr := connection.GetBridgeWssListener()
			if wssErr != nil {
				logs.Error("%v", wssErr)
				os.Exit(0)
				return
			}
			wssLn := conn.NewWSSListener(wssListener, beego.AppConfig.String("bridge_path"), crypt.GetCert())
			conn.Accept(wssLn, func(c net.Conn) {
				s.cliProcess(conn.NewConn(c), "wss")
			})
		}()
	}

	// kcp
	if ServerKcpEnable {
		logs.Info("server start, the bridge type is kcp, the bridge port is %s", connection.BridgeKcpPort)
		go func() {
			bridgeKcp := *s
			bridgeKcp.tunnelType = "kcp"
			err := conn.NewKcpListenerAndProcess(common.BuildAddress(connection.BridgeKcpIp, connection.BridgeKcpPort), func(c net.Conn) {
				bridgeKcp.cliProcess(conn.NewConn(c), "kcp")
			})
			if err != nil {
				logs.Error("KCP listener error: %v", err)
			}
		}()
	}

	// quic
	if ServerQuicEnable {
		logs.Info("server start, the bridge type is quic, the bridge port is %s", connection.BridgeQuicPort)
		go func() {
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{crypt.GetCert()},
			}
			alpnList := beego.AppConfig.DefaultString("quic_alpn", "nps")
			protocols := strings.Split(alpnList, ",")
			tlsCfg.NextProtos = protocols
			addr := common.BuildAddress(connection.BridgeQuicIp, connection.BridgeQuicPort)
			err := conn.NewQuicListenerAndProcess(addr, tlsCfg, func(c net.Conn) {
				s.cliProcess(conn.NewConn(c), "quic")
			})
			if err != nil {
				logs.Error("QUIC listener error: %v", err)
			}
		}()
	}

	return nil
}

// GetHealthFromClient get health information form client
func (s *Bridge) GetHealthFromClient(id int, c *conn.Conn) {
	if id <= 0 {
		return
	}

	const maxRetry = 3
	var retry int
	//firstSuccess := false

	for {
		info, status, err := c.GetHealthInfo(10 * time.Second)
		if err != nil {
			//logs.Trace("GetHealthInfo error, id=%d, retry=%d, err=%v", id, retry, err)
			if conn.IsTempOrTimeout(err) && retry < maxRetry {
				retry++
				continue
			}
			//if !firstSuccess {
			//	return
			//}
			logs.Trace("GetHealthInfo error, id=%d, retry=%d, err=%v", id, retry, err)
			break
		}
		logs.Trace("GetHealthInfo: %v, %v, %v", info, err, status)
		//firstSuccess = true
		retry = 0

		if !status { //the status is true , return target to the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(strings.ReplaceAll(v.Target.TargetStr, "\r\n", "\n"), "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(strings.ReplaceAll(v.Target.TargetStr, "\r\n", "\n"), "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		} else { //the status is false,remove target from the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})

			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		}
	}
	//s.DelClient(id)
	//_ = c.Close()
}

func (s *Bridge) verifyError(c *conn.Conn) {
	if !ServerSecureMode {
		_, _ = c.Write([]byte(common.VERIFY_EER))
	}
	_ = c.Close()
}

func (s *Bridge) verifySuccess(c *conn.Conn) {
	_, _ = c.Write([]byte(common.VERIFY_SUCCESS))
}

func (s *Bridge) cliProcess(c *conn.Conn, tunnelType string) {
	if c.Conn == nil || c.Conn.RemoteAddr() == nil {
		logs.Warn("Invalid connection")
		_ = c.Close()
		return
	}

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

	if ver == 0 {
		// --- protocol 0.26.0 path ---
		//write server version to client
		if _, err := c.Write([]byte(crypt.Md5(version.GetVersion(ver)))); err != nil {
			logs.Error("Failed to write server version to client %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		c.SetReadDeadlineBySecond(5)
		//get vKey from client
		keyBuf, err := c.GetShortContent(32)
		if err != nil {
			logs.Trace("Failed to read vKey from client %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		//verify
		id, err := file.GetDb().GetIdByVerifyKey(string(keyBuf), c.Conn.RemoteAddr().String(), "", crypt.Md5)
		if err != nil {
			logs.Error("Validation error for client %v (proto-ver %d, vKey %x): %v", c.Conn.RemoteAddr(), ver, keyBuf, err)
			s.verifyError(c)
			return
		}
		s.verifySuccess(c)

		flag, err := c.ReadFlag()
		if err != nil {
			logs.Warn("Failed to read operation flag from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		s.typeDeal(flag, c, id, clientVer)
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
		id, err := file.GetDb().GetClientIdByBlake2bVkey(string(keyBuf))
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
		client.Addr = common.GetIpByAddr(c.Conn.RemoteAddr().String())
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
			client.LocalAddr = common.DecodeIP(infoDec).String()
			client.Mode = tunnelType
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
			client.LocalAddr = ip.String()
			tp := string(infoDec[18 : 18+l])
			client.Mode = fmt.Sprintf("%s,%s", tunnelType, tp)
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
			_ = c.WriteLenContent(fpBuf)
			if ver > 3 {
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
		c.SetReadDeadlineBySecond(5)

		flag, err := c.ReadFlag()
		if err != nil {
			logs.Warn("Failed to read operation flag from %v: %v", c.Conn.RemoteAddr(), err)
			_ = c.Close()
			return
		}
		if ver > 3 {
			// --- protocol 0.30.0+ path ---
			_, err := c.GetShortLenContent()
			if err != nil {
				logs.Error("Failed to read random buffer from %v: %v", c.Conn.RemoteAddr(), err)
				_ = c.Close()
				return
			}
		}
		s.typeDeal(flag, c, id, clientVer)
	}
	return
}

func (s *Bridge) DelClient(id int) {
	if v, ok := s.Client.Load(id); ok {
		client := v.(*Client)
		_ = client.Close()

		s.Client.Delete(id)

		if file.GetDb().IsPubClient(id) {
			return
		}
		if c, err := file.GetDb().GetClient(id); err == nil {
			select {
			case s.CloseClient <- c.Id:
			default:
				logs.Warn("CloseClient channel is full, failed to send close signal for client %d", c.Id)
			}
		}
	}
}

// use different
func (s *Bridge) typeDeal(typeVal string, c *conn.Conn, id int, vs string) {
	isPub := file.GetDb().IsPubClient(id)
	switch typeVal {
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
		if v, loaded := s.Client.LoadOrStore(id, NewClient(id, nil, nil, c, vs)); loaded {
			client := v.(*Client)
			//if client.signal != nil {
			//	_, _ = client.signal.WriteClose()
			//	client.signals.LoadOrStore(client.signal, struct{}{})
			//}
			client.AddSignal(c)
			client.Version = vs
		}

		go s.GetHealthFromClient(id, c)
		logs.Info("clientId %d connection succeeded, address:%v ", id, c.Conn.RemoteAddr())

	case common.WORK_CHAN:
		muxConn := nps_mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime)
		if v, loaded := s.Client.LoadOrStore(id, NewClient(id, muxConn, nil, nil, vs)); loaded {
			client := v.(*Client)
			//if client.tunnel != nil {
			//	client.tunnels.LoadOrStore(client.tunnel, struct{}{})
			//}
			client.AddTunnel(muxConn)
		}

	case common.WORK_CONFIG:
		client, err := file.GetDb().GetClient(id)
		if err != nil || (!isPub && !client.ConfigConnAllow) {
			_ = c.Close()
			return
		}
		_ = binary.Write(c, binary.LittleEndian, isPub)
		go s.getConfig(c, isPub, client)

	case common.WORK_REGISTER:
		go s.register(c)
		return

	case common.WORK_SECRET:
		b, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("secret error, failed to match the key successfully")
			_ = c.Close()
			return
		}
		s.SecretChan <- conn.NewSecret(string(b), c)

	case common.WORK_FILE:
		muxConn := nps_mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime)
		if v, loaded := s.Client.LoadOrStore(id, NewClient(id, nil, muxConn, nil, vs)); loaded {
			client := v.(*Client)
			//if client.file != nil {
			//	client.files.LoadOrStore(client.file, struct{}{})
			//}
			client.AddFile(muxConn)
		}

	case common.WORK_P2P:
		// read md5 secret
		b, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("p2p error, %v", err)
			_ = c.Close()
			return
		}
		t := file.GetDb().GetTaskByMd5Password(string(b))
		if t == nil {
			logs.Error("p2p error, failed to match the key successfully")
			_ = c.Close()
			return
		}
		v, ok := s.Client.Load(t.Client.Id)
		if !ok {
			_ = c.Close()
			return
		}
		serverPort := beego.AppConfig.String("p2p_port")
		if serverPort == "" {
			logs.Warn("get local udp addr error")
			_ = c.Close()
			return
		}
		serverIP := common.GetServerIp()
		svrAddr := common.BuildAddress(serverIP, serverPort)
		signalAddr := common.BuildAddress(serverIP, serverPort)
		remoteIP := net.ParseIP(common.GetIpByAddr(c.RemoteAddr().String()))
		if remoteIP != nil && (remoteIP.IsPrivate() || remoteIP.IsLoopback() || remoteIP.IsLinkLocalUnicast()) {
			svrAddr = common.BuildAddress(common.GetIpByAddr(c.LocalAddr().String()), serverPort)
		}
		client := v.(*Client)
		signal := client.GetSignal()
		if signal == nil {
			s.DelClient(t.Client.Id)
			_ = c.Close()
			return
		}
		signalIP := net.ParseIP(common.GetIpByAddr(signal.RemoteAddr().String()))
		if signalIP != nil && (signalIP.IsPrivate() || signalIP.IsLoopback() || signalIP.IsLinkLocalUnicast()) {
			signalAddr = common.BuildAddress(common.GetIpByAddr(signal.LocalAddr().String()), serverPort)
		}
		_, _ = signal.BufferWrite([]byte(common.NEW_UDP_CONN))
		_ = signal.WriteLenContent([]byte(signalAddr))
		_ = signal.WriteLenContent(b)
		if err := signal.FlushBuf(); err != nil {
			logs.Warn("client signal flush error: %v", err)
			_ = signal.Close()
			_ = c.Close()
			return
		}
		_ = c.WriteLenContent([]byte(svrAddr))
		if err := c.FlushBuf(); err != nil {
			logs.Warn("p2p head flush error: %v", err)
		}
		logs.Trace("P2P: remoteIP=%s, svr1Addr=%s, clientIP=%s, svr2Addr=%s", remoteIP, svrAddr, signalIP, signalAddr)
		_ = c.Close()
		return
	}

	c.SetAlive()
	return
}

// register ip
func (s *Bridge) register(c *conn.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hour int32
	if err := binary.Read(c, binary.LittleEndian, &hour); err == nil {
		ip := common.GetIpByAddr(c.Conn.RemoteAddr().String())
		s.Register.Store(ip, time.Now().Add(time.Hour*time.Duration(hour)))
		logs.Info("Registered IP: %s for %d hours", ip, hour)
	} else {
		logs.Warn("Failed to register IP: %v", err)
	}
}

func (s *Bridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error) {
	clientValue, ok := s.Client.Load(clientId)
	if !ok {
		err = errors.New(fmt.Sprintf("the client %d is not connect", clientId))
		return
	}

	// if the proxy type is local
	if link.LocalProxy {
		network := "tcp"
		if link.ConnType == common.CONN_UDP {
			network = "udp"
		}
		target, err = net.Dial(network, common.FormatAddress(link.Host))
		return
	}

	client := clientValue.(*Client)
	// If IP is restricted, do IP verification
	if s.ipVerify {
		ip := common.GetIpByAddr(link.RemoteAddr)
		ipValue, ok := s.Register.Load(ip)
		if !ok {
			return nil, errors.New(fmt.Sprintf("The ip %s is not in the validation list", ip))
		}

		if !ipValue.(time.Time).After(time.Now()) {
			return nil, errors.New(fmt.Sprintf("The validity of the ip %s has expired", ip))
		}
	}

	var tunnel *nps_mux.Mux
	if t != nil && t.Mode == "file" {
		tunnel = client.GetFile()
	} else {
		tunnel = client.GetTunnel()
	}

	if tunnel == nil {
		s.DelClient(clientId)
		err = errors.New("the client connect error")
		return
	}

	target, err = tunnel.NewConn()
	if err != nil {
		return
	}

	if t != nil && t.Mode == "file" {
		//TODO if t.mode is file ,not use crypt or compress
		link.Crypt = false
		link.Compress = false
		return
	}

	if _, err = conn.NewConn(target).SendInfo(link, ""); err != nil {
		logs.Info("new connection error, the target %s refused to connect", link.Host)
		return
	}

	return
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			closedClients := make([]int, 0)

			s.Client.Range(func(key, value interface{}) bool {
				clientID := key.(int)
				if clientID <= 0 {
					return true
				}
				client, ok := value.(*Client)
				if !ok || client == nil {
					logs.Trace("Client %d is nil", clientID)
					closedClients = append(closedClients, clientID)
					return true
				}
				sig := client.signal.Load()
				tun := client.tunnel.Load()
				if (sig == nil || sig.IsClosed()) || (tun == nil || tun.IsClosed()) {
					client.retryTime++
					if client.retryTime >= 3 {
						logs.Trace("Stop client %d", clientID)
						closedClients = append(closedClients, clientID)
					}
					client.SwitchSignal()
					client.SwitchTunnel()
					client.SwitchFile()
				} else {
					client.retryTime = 0 // Reset retry count when the state is normal
				}
				return true
			})

			for _, clientId := range closedClients {
				logs.Info("the client %d closed", clientId)
				s.DelClient(clientId)
			}
		}
	}
}

// get config and add task from client config
func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *file.Client) {
	var fail bool
loop:
	for {
		flag, err := c.ReadFlag()
		if err != nil {
			break
		}

		switch flag {
		case common.WORK_STATUS:
			b, err := c.GetShortContent(64)
			if err != nil {
				break loop
			}

			id, err := file.GetDb().GetClientIdByBlake2bVkey(string(b))
			if err != nil {
				break loop
			}

			var strBuilder strings.Builder
			if client.IsConnect && !isPub {
				file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
					v := value.(*file.Host)
					if v.Client.Id == id {
						strBuilder.WriteString(v.Remark + common.CONN_DATA_SEQ)
					}
					return true
				})

				file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
					v := value.(*file.Tunnel)
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						strBuilder.WriteString(v.Remark + common.CONN_DATA_SEQ)
					}
					return true
				})
			}
			str := strBuilder.String()
			_ = binary.Write(c, binary.LittleEndian, int32(len([]byte(str))))
			_ = binary.Write(c, binary.LittleEndian, []byte(str))
			break loop

		case common.NEW_CONF:
			client, err = c.GetConfigInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			if err = file.GetDb().NewClient(client); err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			_ = c.WriteAddOk()
			_, _ = c.Write([]byte(client.VerifyKey))
			s.Client.Store(client.Id, NewClient(client.Id, nil, nil, nil, ""))

		case common.NEW_HOST:
			h, err := c.GetHostInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			h.Client = client
			if h.Location == "" {
				h.Location = "/"
			}

			if !client.HasHost(h) {
				if file.GetDb().IsHostExist(h) {
					fail = true
					_ = c.WriteAddFail()
					break loop
				}
				_ = file.GetDb().NewHost(h)
			}
			_ = c.WriteAddOk()

		case common.NEW_TASK:
			t, err := c.GetTaskInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			ports := common.GetPorts(t.Ports)
			targets := common.GetPorts(t.Target.TargetStr)
			if len(ports) > 1 && (t.Mode == "tcp" || t.Mode == "udp") && (len(ports) != len(targets)) {
				fail = true
				_ = c.WriteAddFail()
				break loop
			} else if t.Mode == "secret" || t.Mode == "p2p" {
				ports = append(ports, 0)
			}

			if len(ports) == 0 {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			for i := 0; i < len(ports); i++ {
				tl := &file.Tunnel{
					Mode:         t.Mode,
					Port:         ports[i],
					ServerIp:     t.ServerIp,
					Client:       client,
					Password:     t.Password,
					LocalPath:    t.LocalPath,
					StripPre:     t.StripPre,
					MultiAccount: t.MultiAccount,
					Id:           int(file.GetDb().JsonDb.GetTaskId()),
					Status:       true,
					Flow:         new(file.Flow),
					NoStore:      true,
				}

				if len(ports) == 1 {
					tl.Target = t.Target
					tl.Remark = t.Remark
				} else {
					tl.Remark = fmt.Sprintf("%s_%d", t.Remark, tl.Port)
					if t.TargetAddr != "" {
						tl.Target = &file.Target{
							TargetStr: fmt.Sprintf("%s:%d", t.TargetAddr, targets[i]),
						}
					} else {
						tl.Target = &file.Target{
							TargetStr: strconv.Itoa(targets[i]),
						}
					}
				}

				if !client.HasTunnel(tl) {
					if err := file.GetDb().NewTask(tl); err != nil {
						logs.Warn("add task error: %v", err)
						fail = true
						_ = c.WriteAddFail()
						break loop
					}

					if b := tool.TestServerPort(tl.Port, tl.Mode); !b && t.Mode != "secret" && t.Mode != "p2p" {
						fail = true
						_ = c.WriteAddFail()
						break loop
					}

					s.OpenTask <- tl
				}
				_ = c.WriteAddOk()
			}
		}
	}

	if fail && client != nil {
		s.DelClient(client.Id)
	}
	_ = c.Close()
}
