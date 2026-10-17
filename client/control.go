package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/net/proxy"
)

const MaxPad = 64

var Ver = version.GetLatestIndex()
var SkipTLSVerify = false
var DisableP2P = false
var AutoReconnect = true
var LocalIPForward = false

var TlsCfg = &tls.Config{
	InsecureSkipVerify: true,
	ServerName:         crypt.GetFakeDomainName(),
	NextProtos:         []string{"h3"},
}

var QuicConfig = &quic.Config{
	KeepAlivePeriod:    10 * time.Second,
	MaxIdleTimeout:     30 * time.Second,
	MaxIncomingStreams: 100000,
}

func init() {
	//rand.Seed(time.Now().UnixNano())
	crypt.InitTls(tls.Certificate{})
}

func GetTaskStatus(server string, vKey string, tp string, proxyUrl string, localIP string) {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	//defer c.Close()
	err = SendType(c, common.WORK_CONFIG, uuid)
	if err != nil {
		log.Fatalf("Failed to send type: %v", err)
	}
	if _, err := c.BufferWrite([]byte(common.WORK_STATUS)); err != nil {
		log.Fatalf("Failed to write WORK_STATUS: %v", err)
	}
	if _, err := c.Write([]byte(crypt.Blake2b(vKey))); err != nil {
		log.Fatalf("Failed to write auth key: %v", err)
	}
	var isPub bool
	_ = binary.Read(c, binary.LittleEndian, &isPub)
	length, err := c.GetLen()
	//log.Println(length)
	if err != nil {
		log.Fatalf("Failed to read length: %v", err)
	}
	data, err := c.GetShortContent(length)
	//log.Println(string(data))
	if err != nil {
		log.Fatalf("Failed to read content: %v", err)
	}
	parts := strings.Split(string(data), common.CONN_DATA_SEQ)
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	fmt.Println("===== Active Tunnels/Hosts =====")
	fmt.Printf("Total active: %d\n", len(parts))
	for i, name := range parts {
		display := name
		if display == "" {
			display = "(no remark)"
		}
		fmt.Printf("  %d. %s\n", i+1, display)
	}
	os.Exit(0)
}

func RegisterLocalIp(server string, vKey string, tp string, proxyUrl string, localIP string, hour int) {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		log.Fatalln(err)
	}
	//defer c.Close()
	err = SendType(c, common.WORK_REGISTER, uuid)
	if err != nil {
		log.Fatalln(err)
	}
	if err := binary.Write(c, binary.LittleEndian, int32(hour)); err != nil {
		log.Fatalln(err)
	}
	log.Printf("Successful ip registration for local public network, the validity period is %d hours.", hour)
	os.Exit(0)
}

var errAdd = errors.New("the server returned an error, which port or host may have been occupied or not allowed to open")

func StartFromFile(pCtx context.Context, pCancel context.CancelFunc, path string) {
	cnf, err := config.NewConfig(path)
	if err != nil || cnf.CommonConfig == nil {
		logs.Error("Config file %s loading error %v", path, err)
		os.Exit(1)
	}
	logs.Info("Loading configuration file %s successfully", path)

	common.SetCustomDNS(cnf.CommonConfig.DnsServer)

	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetLatest())

	common.SetNtpServer(cnf.CommonConfig.NtpServer)
	if cnf.CommonConfig.NtpInterval > 0 {
		common.SetNtpInterval(time.Duration(cnf.CommonConfig.NtpInterval) * time.Minute)
	}
	common.SyncTime()

	var uuid string

	first := true
	for {
		select {
		case <-pCtx.Done():
			return
		default:
		}
		if !first && (!cnf.CommonConfig.AutoReconnection || !AutoReconnect) {
			pCancel()
			os.Exit(1)
			return
		}
		if !first {
			logs.Info("Reconnecting...")
			time.Sleep(time.Second * 5)
		}
		first = false

		if cnf.CommonConfig.TlsEnable {
			cnf.CommonConfig.Tp = "tls"
		}

		if len(cnf.LocalServer) > 0 {
			p2pm := NewP2PManager(pCtx, pCancel, cnf.CommonConfig)
			//create local server secret or p2p
			for _, v := range cnf.LocalServer {
				vv := v
				go func(local *config.LocalServer) { _ = p2pm.StartLocalServer(local) }(vv)
			}
			return
		}

		c, cid, err := NewConn(cnf.CommonConfig.Tp, cnf.CommonConfig.VKey, cnf.CommonConfig.Server, cnf.CommonConfig.ProxyUrl, cnf.CommonConfig.LocalIP)
		if err != nil {
			logs.Error("Failed to connect: %v", err)
			continue
		}
		if uuid == "" {
			uuid = cid
		}
		err = SendType(c, common.WORK_CONFIG, uuid)
		if err != nil {
			logs.Error("Failed to send type: %v", err)
			_ = c.Close()
			continue
		}

		var isPub bool
		_ = binary.Read(c, binary.LittleEndian, &isPub)

		// get tmp password
		var b []byte
		vkey := cnf.CommonConfig.VKey
		if isPub {
			// send global configuration to server and get status of config setting
			if _, err := c.SendInfo(cnf.CommonConfig.Client, common.NEW_CONF); err != nil {
				logs.Error("%v", err)
				_ = c.Close()
				continue
			}
			if !c.GetAddStatus() {
				logs.Error("the web_user may have been occupied!")
				_ = c.Close()
				continue
			}

			if b, err = c.GetShortContent(16); err != nil {
				logs.Error("%v", err)
				_ = c.Close()
				continue
			}
			vkey = string(b)
		}

		//if err := ioutil.WriteFile(filepath.Join(common.GetTmpPath(), "npc_vkey.txt"), []byte(vkey), 0600); err != nil {
		//	logs.Debug("Failed to write vkey file: %v", err)
		//	c.Close()
		//	continue
		//}

		//send hosts to server
		for _, v := range cnf.Hosts {
			if _, err := c.SendInfo(v, common.NEW_HOST); err != nil {
				logs.Error("%v", err)
				continue
			}
			if !c.GetAddStatus() {
				logs.Error("%v %s", errAdd, v.Host)
				continue
			}
		}

		ctx, cancel := context.WithCancel(pCtx)
		fsm := NewFileServerManager(ctx)

		//send  task to server
		for _, v := range cnf.Tasks {
			if _, err := c.SendInfo(v, common.NEW_TASK); err != nil {
				logs.Error("%v", err)
				continue
			}
			if !readTaskAddStatus(c, v) {
				logs.Error("%v %s %s", errAdd, v.Ports, v.Remark)
				continue
			}
			if v.Mode == "file" {
				//start local file server
				go fsm.StartFileServer(v, vkey)
			}
		}
		_ = c.Close()
		if cnf.CommonConfig.Client.WebUserName == "" || cnf.CommonConfig.Client.WebPassword == "" {
			logs.Info("web access login username:user password:%s", vkey)
		} else {
			logs.Info("web access login username:%s password:%s", cnf.CommonConfig.Client.WebUserName, cnf.CommonConfig.Client.WebPassword)
		}

		NewRPClient(cnf.CommonConfig.Server, vkey, cnf.CommonConfig.Tp, cnf.CommonConfig.ProxyUrl, cnf.CommonConfig.LocalIP, uuid, cnf, cnf.CommonConfig.DisconnectTime, fsm).Start(ctx)
		fsm.CloseAll()
		cancel()
	}
}

// readTaskAddStatus reads the add statuses the server reports for a NEW_TASK:
// one per port the task expands to, or a single failure. It stops at the first
// failure, after which the server closes the connection.
func readTaskAddStatus(c *conn.Conn, t *file.Tunnel) bool {
	n := 1
	if ports, _, ok := file.ConfigTaskPorts(t); ok {
		n = len(ports)
	}
	for i := 0; i < n; i++ {
		if !c.GetAddStatus() {
			return false
		}
	}
	return true
}

func VerifyState(state tls.ConnectionState, host string) (fingerprint []byte, verified bool) {
	if len(state.PeerCertificates) == 0 {
		return nil, false
	}
	leaf := state.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		inter.AddCert(cert)
	}
	roots, _ := x509.SystemCertPool()
	opts := x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots,
		Intermediates: inter,
	}
	if _, err := leaf.Verify(opts); err != nil {
		verified = false
	} else {
		verified = true
	}
	sum := sha256.Sum256(leaf.Raw)
	return sum[:], verified
}

func VerifyTLS(connection net.Conn, host string) (fingerprint []byte, verified bool) {
	var tlsConn *tls.Conn
	if tc, ok := connection.(*conn.TlsConn); ok {
		tlsConn = tc.Conn
	} else if std, ok := connection.(*tls.Conn); ok {
		tlsConn = std
	} else {
		return nil, false
	}
	if err := tlsConn.Handshake(); err != nil {
		return nil, false
	}
	return VerifyState(tlsConn.ConnectionState(), host)
}

func EnsurePort(server string, tp string) string {
	_, port, err := net.SplitHostPort(server)
	if err == nil && port != "" {
		return server
	}
	if p, ok := common.DefaultPort[tp]; ok {
		return net.JoinHostPort(server, p)
	}
	return server
}

// NewConn Create a new connection with the server and verify it
func NewConn(tp string, vkey string, server string, proxyUrl string, localIP string) (*conn.Conn, string, error) {
	c, uuid, _, err := dialServer(tp, vkey, server, proxyUrl, localIP)
	return c, uuid, err
}

// serverInfo is what a verified bridge connection tells about the server.
type serverInfo struct {
	// fingerprint is the SHA-256 of the bridge certificate, sent encrypted
	// with the vkey during the handshake; nil before protocol 0.28.0.
	fingerprint []byte
	// ip is the server address the connection reached, empty when it went
	// through a proxy (proxy_url, or a WebSocket bridge that may sit behind
	// a reverse proxy or CDN).
	ip string
}

// dialServer is NewConn returning the server information as well.
func dialServer(tp string, vkey string, server string, proxyUrl string, localIP string) (*conn.Conn, string, serverInfo, error) {
	var info serverInfo
	//logs.Debug("NewConn: %s %s %s %s %s", tp, vkey, server, connType, proxyUrl)
	var err error
	var connection net.Conn
	var sess *kcp.UDPSession
	var path string
	var isTls = false
	var tlsVerify = false
	var tlsFp []byte

	timeout := time.Second * 10
	alpn := "nps"
	server, path = common.SplitServerAndPath(server)
	if path == "" {
		path = "/ws"
	} else {
		alpn = strings.TrimSpace(strings.TrimPrefix(path, "/"))
	}
	addr, host, sni := common.SplitAddrAndHost(server)
	server = EnsurePort(addr, tp)

	if HasFailed {
		if s, e := common.GetFastAddr(server, tp); e == nil {
			server = s
			//logs.Debug("Fast Server: %s", server)
		} else {
			logs.Debug("Server: %s Path: %s Error: %v", server, path, e)
		}
	}

	switch tp {
	case "tcp":
		connection, err = GetProxyConn(proxyUrl, server, timeout, localIP)
	case "tls":
		isTls = true
		conf := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
		}
		rawConn, err := GetProxyConn(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection, err = conn.NewTlsConn(rawConn, timeout, conf)
		if err != nil {
			_ = rawConn.Close()
			return nil, "", serverInfo{}, err
		}
		tlsFp, tlsVerify = VerifyTLS(connection, sni)
	case "ws":
		rawConn, err := GetProxyConn(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		urlStr := "ws://" + server + path
		//logs.Debug("URL: %s", urlStr)
		wsConn, resp, err := conn.DialWS(rawConn, urlStr, host, timeout)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			_ = rawConn.Close()
			return nil, "", serverInfo{}, err
		}
		connection = conn.NewWsConn(wsConn)
	case "wss":
		isTls = true
		urlStr := "wss://" + server + path
		//logs.Debug("URL: %s Host: %s SNI: %s", urlStr, host, sni)
		rawConn, err := GetProxyConn(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		wsConn, resp, err := conn.DialWSS(rawConn, urlStr, host, sni, timeout)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			_ = rawConn.Close()
			return nil, "", serverInfo{}, err
		}
		if underlying := wsConn.NetConn(); underlying != nil {
			tlsFp, tlsVerify = VerifyTLS(underlying, sni)
		}
		connection = conn.NewWsConn(wsConn)
	case "quic":
		isTls = true
		tlsCfg := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
			NextProtos:         []string{alpn},
		}
		ctx := context.Background()
		sess, err := conn.DialQuicWithLocalIP(ctx, server, tlsCfg, QuicConfig, localIP)
		if err != nil {
			return nil, "", serverInfo{}, fmt.Errorf("quic dial error: %w", err)
		}
		state := sess.ConnectionState().TLS
		tlsFp, tlsVerify = VerifyState(state, sni)
		stream, err := sess.OpenStreamSync(ctx)
		if err != nil {
			_ = sess.CloseWithError(0, "")
			return nil, "", serverInfo{}, fmt.Errorf("quic open stream error: %w", err)
		}
		connection = conn.NewQuicAutoCloseConn(stream, sess)
	default:
		sess, err = conn.DialKCPWithLocalIP(server, localIP)
		if err == nil {
			connection = sess
		}
	}

	if connection == nil {
		return nil, "", serverInfo{}, fmt.Errorf("newConn: unexpected nil connection for tp=%q server=%q", tp, server)
	}

	if err != nil {
		_ = connection.Close()
		return nil, "", serverInfo{}, err
	}

	//logs.Debug("SetDeadline")
	_ = connection.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = connection.SetDeadline(time.Time{}) }()

	c := conn.NewConn(connection)
	if c == nil {
		_ = connection.Close()
		return nil, "", serverInfo{}, fmt.Errorf("conn.NewConn returned nil (tp=%q server=%q)", tp, server)
	}
	if _, err := c.BufferWrite([]byte(common.CONN_TEST)); err != nil {
		_ = c.Close()
		return nil, "", serverInfo{}, err
	}
	minVerBytes := []byte(version.GetVersion(Ver))
	if err := c.WriteLenContent(minVerBytes); err != nil {
		_ = c.Close()
		return nil, "", serverInfo{}, err
	}
	vs := []byte(version.VERSION)
	padLen := rand.Intn(MaxPad)
	if padLen > 0 {
		vs = append(vs, make([]byte, padLen)...)
	}
	if err := c.WriteLenContent(vs); err != nil {
		_ = c.Close()
		return nil, "", serverInfo{}, err
	}
	var uuid string
	if Ver == 0 {
		// 0.26.0
		b, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("%v", err)
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		if crypt.Md5(version.GetVersion(Ver)) != string(b) {
			logs.Warn("The client does not match the server version. The current core version of the client is %s", version.GetVersion(Ver))
			//_ = c.Close()
			//return nil, err
		}
		if _, err := c.BufferWrite([]byte(crypt.Md5(vkey))); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		if s, err := c.ReadFlag(); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		} else if s == common.VERIFY_EER {
			_ = c.Close()
			return nil, "", serverInfo{}, fmt.Errorf("validation key %s incorrect", vkey)
		}
	} else {
		// 0.27.0
		ts := common.TimeNow().Unix() - int64(rand.Intn(6))
		if _, err := c.BufferWrite(common.TimestampToBytes(ts)); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		if _, err := c.BufferWrite([]byte(crypt.Blake2b(vkey))); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		var infoBuf []byte
		if Ver < 3 {
			// 0.27.0 0.28.0
			var err error
			infoBuf, err = crypt.EncryptBytes(common.EncodeIP(common.GetOutboundIP()), vkey)
			if err != nil {
				_ = c.Close()
				return nil, "", serverInfo{}, err
			}
		} else {
			// 0.29.0
			ipPart := common.EncodeIP(common.GetOutboundIP()) // 17bit
			tpBytes := []byte(tp)
			tpLen := len(tpBytes)
			if tpLen > 32 {
				_ = c.Close()
				return nil, "", serverInfo{}, fmt.Errorf("tp too long: %d bytes (max %d)", tpLen, 32)
			}
			length := byte(tpLen)
			// IP(17 bit) + len(1 bit) + tpBytes
			buf := make([]byte, 0, len(ipPart)+1+len(tpBytes))
			buf = append(buf, ipPart...)
			buf = append(buf, length)
			buf = append(buf, tpBytes...)
			var err error
			infoBuf, err = crypt.EncryptBytes(buf, vkey)
			if err != nil {
				_ = c.Close()
				return nil, "", serverInfo{}, err
			}
		}
		if err := c.WriteLenContent(infoBuf); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		randBuf, err := common.RandomBytes(1000)
		if err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		if err := c.WriteLenContent(randBuf); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		hmacBuf := crypt.ComputeHMAC(vkey, ts, minVerBytes, vs, infoBuf, randBuf)
		if _, err := c.BufferWrite(hmacBuf); err != nil {
			_ = c.Close()
			return nil, "", serverInfo{}, err
		}
		b, err := c.GetShortContent(32)
		if err != nil {
			logs.Error("error reading server response: %v", err)
			_ = c.Close()
			return nil, "", serverInfo{}, fmt.Errorf("validation key %s incorrect", vkey)
		}
		if !bytes.Equal(b, crypt.ComputeHMAC(vkey, ts, hmacBuf, []byte(version.GetVersion(Ver)))) {
			logs.Warn("The client does not match the server version. The current core version of the client is %s", version.GetVersion(Ver))
			_ = c.Close()
			return nil, "", serverInfo{}, fmt.Errorf("the client does not match the server version %s", version.GetVersion(Ver))
		}
		if Ver > 1 {
			fpBuf, err := c.GetShortLenContent()
			if err != nil {
				_ = c.Close()
				return nil, "", serverInfo{}, err
			}
			fpDec, err := crypt.DecryptBytes(fpBuf, vkey)
			if err != nil {
				_ = c.Close()
				return nil, "", serverInfo{}, err
			}
			if !SkipTLSVerify && isTls && !tlsVerify && !bytes.Equal(fpDec, tlsFp) {
				logs.Warn("Certificate verification failed. To skip verification, please set -skip_verify=true")
				_ = c.Close()
				return nil, "", serverInfo{}, errors.New("validation cert incorrect")
			}
			crypt.AddTrustedCert(vkey, fpDec)
			info.fingerprint = fpDec
			if Ver > 3 {
				// v0.30.0
				if Ver > 5 {
					// v0.32.0
					uuidBuf, err := c.GetShortLenContent()
					if err != nil {
						_ = c.Close()
						return nil, "", serverInfo{}, err
					}
					uuid = string(uuidBuf)
				}
				_, err := c.GetShortLenContent()
				if err != nil {
					_ = c.Close()
					return nil, "", serverInfo{}, err
				}
			}
		}
	}
	if proxyUrl == "" && tp != common.CONN_WS && tp != common.CONN_WSS {
		if ap, perr := netip.ParseAddrPort(connection.RemoteAddr().String()); perr == nil {
			info.ip = ap.Addr().Unmap().String()
		}
	}
	//_, err = SendType(c, connType, uuid)
	return c, uuid, info, err
}

func SendType(c *conn.Conn, connType, uuid string) error {
	if c == nil {
		return fmt.Errorf("sendType: nil conn (connType=%s uuid=%s)", connType, uuid)
	}
	if _, err := c.BufferWrite([]byte(connType)); err != nil {
		_ = c.Close()
		return err
	}
	if Ver > 3 {
		// v0.30.0
		if Ver > 5 {
			// v0.32.0
			if err := c.WriteLenContent([]byte(uuid)); err != nil {
				_ = c.Close()
				return err
			}
		}
		randByte, err := common.RandomBytes(1000)
		if err != nil {
			_ = c.Close()
			return err
		}
		if err := c.WriteLenContent(randByte); err != nil {
			_ = c.Close()
			return err
		}
	}
	if err := c.FlushBuf(); err != nil {
		_ = c.Close()
		return err
	}
	c.SetAlive()
	return nil
}

// GetProxyConn dials server directly, through proxyUrl, or through the proxy
// configured in the environment. The whole dial, including any proxy
// handshake, is bounded by timeout.
func GetProxyConn(proxyUrl, server string, timeout time.Duration, localIP string) (net.Conn, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	dialer := &net.Dialer{Timeout: timeout, LocalAddr: common.BuildTCPBindAddr(localIP)}
	if proxyUrl == "" {
		return dialProxyContext(ctx, proxy.FromEnvironmentUsing(dialer), server)
	}
	u, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "socks5" {
		d, err := proxy.FromURL(u, dialer)
		if err != nil {
			return nil, err
		}
		return dialProxyContext(ctx, d, server)
	}
	return newHttpProxyConnContext(ctx, dialer, u, server)
}

// dialProxyContext dials through d honouring ctx. Every dialer built by
// golang.org/x/net/proxy implements ContextDialer, which applies the ctx
// deadline to the SOCKS5 handshake as well.
func dialProxyContext(ctx context.Context, d proxy.Dialer, server string) (net.Conn, error) {
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, "tcp", server)
	}
	return d.Dial("tcp", server)
}

// NewHttpProxyConn http proxy connection
func NewHttpProxyConn(proxyURL *url.URL, remoteAddr string, timeout time.Duration, localIP string) (net.Conn, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	dialer := &net.Dialer{Timeout: timeout, LocalAddr: common.BuildTCPBindAddr(localIP)}
	return newHttpProxyConnContext(ctx, dialer, proxyURL, remoteAddr)
}

func newHttpProxyConnContext(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, remoteAddr string) (net.Conn, error) {
	proxyConn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = proxyConn.SetDeadline(deadline)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: remoteAddr},
		Host:   remoteAddr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		username := proxyURL.User.Username()
		password, _ := proxyURL.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	}
	if err := req.Write(proxyConn); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(proxyConn), req)
	if err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = proxyConn.Close()
		return nil, errors.New("proxy CONNECT failed: " + resp.Status)
	}
	_ = proxyConn.SetDeadline(time.Time{})
	return proxyConn, nil
}

// get a basic auth string
//func getBasicAuth(username, password string) string {
//	auth := username + ":" + password
//	return base64.StdEncoding.EncodeToString([]byte(auth))
//}
