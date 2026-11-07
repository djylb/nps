package client

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxydial"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

const MaxPad = 64

const dialTimeout = 10 * time.Second

var Ver = legacy.LatestIndex
var SkipTLSVerify = false
var DisableP2P = false
var AutoReconnect = true
var LocalIPForward = false

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
	err = SendType(c, legacy.WorkConfig, uuid)
	if err != nil {
		log.Fatalf("Failed to send type: %v", err)
	}
	if _, err := c.BufferWrite([]byte(legacy.WorkStatus)); err != nil {
		log.Fatalf("Failed to write WorkStatus: %v", err)
	}
	if _, err := c.Write([]byte(legacy.KeyDigest(vKey))); err != nil {
		log.Fatalf("Failed to write auth key: %v", err)
	}
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		log.Fatalf("Failed to read status: %v", err)
	}
	parts, err := legacy.ReadStatusList(c)
	if err != nil {
		log.Fatalf("Failed to read status list: %v", err)
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
	err = SendType(c, legacy.WorkRegister, uuid)
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

// WaitReconnect waits for the time before the next connection attempt. It
// returns false if ctx ended first.
func WaitReconnect(ctx context.Context) bool {
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func StartFromFile(pCtx context.Context, pCancel context.CancelFunc, path string) {
	cnf, err := config.NewConfig(path)
	if err != nil || cnf.CommonConfig == nil {
		logs.Error("Config file %s loading error %v", path, err)
		os.Exit(1)
	}
	logs.Info("Loading configuration file %s successfully", path)

	common.SetCustomDNS(cnf.CommonConfig.DnsServer)

	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, legacy.CoreVersion(legacy.LatestIndex))

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
			if !WaitReconnect(pCtx) {
				return
			}
		}
		first = false

		uuid, err = RunConfigOnce(pCtx, cnf, uuid)
		if err != nil {
			logs.Error("%v", err)
		}
	}
}

// RunConfigOnce runs cnf until the session closes or ctx ends and returns the
// client uuid to pass to the next run. Local servers run until ctx ends or one
// of them fails to start.
func RunConfigOnce(ctx context.Context, cnf *config.Config, uuid string) (string, error) {
	if cnf.CommonConfig.TlsEnable {
		cnf.CommonConfig.Tp = "tls"
	}
	if len(cnf.LocalServer) > 0 {
		return uuid, runLocalServers(ctx, cnf)
	}
	c, cid, err := NewConn(cnf.CommonConfig.Tp, cnf.CommonConfig.VKey, cnf.CommonConfig.Server, cnf.CommonConfig.ProxyUrl, cnf.CommonConfig.LocalIP)
	if err != nil {
		return uuid, fmt.Errorf("failed to connect: %w", err)
	}
	if uuid == "" {
		uuid = cid
	}
	if err = SendType(c, legacy.WorkConfig, uuid); err != nil {
		_ = c.Close()
		return uuid, fmt.Errorf("failed to send type: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fsm := NewFileServerManager(ctx)
	defer fsm.CloseAll()
	deadline := time.Now().Add(configExchangeTimeout(cnf))
	vkey, err := exchangeConfig(c, cnf, fsm, deadline)
	if err != nil {
		_ = c.Close()
		return uuid, fmt.Errorf("config exchange failed: %w", err)
	}
	_ = c.Close()
	if cnf.CommonConfig.Client.WebUserName == "" || cnf.CommonConfig.Client.WebPassword == "" {
		logs.Info("web access login username:user password:%s", vkey)
	} else {
		logs.Info("web access login username:%s password:%s", cnf.CommonConfig.Client.WebUserName, cnf.CommonConfig.Client.WebPassword)
	}

	NewRPClient(cnf.CommonConfig.Server, vkey, cnf.CommonConfig.Tp, cnf.CommonConfig.ProxyUrl, cnf.CommonConfig.LocalIP, uuid, cnf, cnf.CommonConfig.DisconnectTime, fsm).Start(ctx)
	return uuid, nil
}

func runLocalServers(ctx context.Context, cnf *config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	mgr := NewP2PManager(ctx, cancel, cnf.CommonConfig)
	defer mgr.Close()
	for _, l := range cnf.LocalServer {
		if err := mgr.StartLocalServer(l); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

// configExchangeTimeout bounds a whole config exchange; the server handles
// the entries one at a time, so it grows with their number.
func configExchangeTimeout(cnf *config.Config) time.Duration {
	return 30*time.Second + time.Duration(len(cnf.Hosts)+len(cnf.Tasks))*5*time.Second
}

// exchangeConfig sends the configuration on c, finished by deadline, and
// returns the vkey to run with. A refused host or task is logged and skipped;
// any error means the connection is unusable and the client must reconnect.
func exchangeConfig(c *conn.Conn, cnf *config.Config, fsm *FileServerManager, deadline time.Time) (string, error) {
	if err := c.SetDeadline(deadline); err != nil {
		return "", err
	}
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		return "", err
	}

	// get tmp password
	vkey := cnf.CommonConfig.VKey
	if isPub {
		// send global configuration to server and get status of config setting
		if err := legacy.WriteClient(c, cnf.CommonConfig.Client); err != nil {
			return "", err
		}
		ok, err := legacy.ReadStatus(c)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("the web_user may have been occupied")
		}
		b, err := legacy.ReadExact(c, 16)
		if err != nil {
			return "", err
		}
		vkey = string(b)
	}

	//send hosts to server
	for _, v := range cnf.Hosts {
		if err := legacy.WriteHost(c, v); err != nil {
			return "", err
		}
		ok, err := legacy.ReadStatus(c)
		if err != nil {
			return "", err
		}
		if !ok {
			logs.Error("%v %s", errAdd, v.Host)
		}
	}

	//send task to server
	for _, v := range cnf.Tasks {
		if err := legacy.WriteTask(c, v); err != nil {
			return "", err
		}
		ok, err := readTaskAddStatus(c, v)
		if err != nil {
			return "", err
		}
		if !ok {
			logs.Error("%v %s %s", errAdd, v.Ports, v.Remark)
			continue
		}
		if v.Mode == "file" {
			//start local file server
			go fsm.StartFileServer(v, vkey)
		}
	}
	return vkey, nil
}

// readTaskAddStatus reads the add statuses the server reports for a FlagNewTask:
// one per port the task expands to, ending at the first failure.
func readTaskAddStatus(c *conn.Conn, t *model.Tunnel) (bool, error) {
	n := 1
	if ports, _, ok := model.ConfigTaskPorts(t); ok {
		n = len(ports)
	}
	for i := 0; i < n; i++ {
		ok, err := legacy.ReadStatus(c)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
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
	sum := tlsconn.Fingerprint(leaf.Raw)
	return sum[:], verified
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
	alpn := "nps"
	server, path := common.SplitServerAndPath(server)
	if path == "" {
		path = "/ws"
	} else {
		alpn = strings.TrimSpace(strings.TrimPrefix(path, "/"))
	}
	addr, host, sni := common.SplitAddrAndHost(server)
	server = EnsurePort(addr, tp)

	var (
		c    *conn.Conn
		uuid string
		info serverInfo
		err  error
	)
	for _, target := range dialTargets(tp, server) {
		c, uuid, info, err = dialBridge(tp, vkey, target, path, alpn, host, sni, proxyUrl, localIP)
		if err == nil {
			return c, uuid, info, nil
		}
		logs.Debug("Dial %s %s failed: %v", tp, target, err)
	}
	return nil, "", serverInfo{}, err
}

// dialTargets lists the addresses to try for server. TCP dials try every
// address of a name themselves. A UDP dial takes only the first one, and only
// the handshake tells whether it is alive, so UDP transports try each in turn.
func dialTargets(tp, server string) []string {
	switch tp {
	case "tcp", common.ConnTLS, common.ConnWS, common.ConnWSS:
		return []string{server}
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil || net.ParseIP(host) != nil {
		return []string{server}
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		// The dial reports the lookup error.
		return []string{server}
	}
	slices.SortStableFunc(ips, func(a, b net.IP) int {
		return cmp.Compare(ipFamilyOrder(a), ipFamilyOrder(b))
	})
	targets := make([]string, len(ips))
	for i, ip := range ips {
		targets[i] = net.JoinHostPort(ip.String(), port)
	}
	return targets
}

func ipFamilyOrder(ip net.IP) int {
	if ip.To4() != nil {
		return 0
	}
	return 1
}

// dialBridge connects to one server address and verifies the connection.
func dialBridge(tp, vkey, server, path, alpn, host, sni, proxyUrl, localIP string) (*conn.Conn, string, serverInfo, error) {
	var err error
	var connection net.Conn
	var sess *kcp.UDPSession
	var isTls = false
	var tlsVerify = false
	var tlsFp []byte

	timeout := dialTimeout

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
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		tlsConn, err := tlsconn.Client(ctx, rawConn, conf)
		cancel()
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection = tlsConn
		tlsFp, tlsVerify = VerifyState(tlsConn.ConnectionState(), sni)
	case "ws":
		rawConn, err := GetProxyConn(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection, err = conn.DialWS(rawConn, "ws://"+server+path, host, timeout)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
	case "wss":
		isTls = true
		rawConn, err := GetProxyConn(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		tlsConn, err := tlsconn.Client(ctx, rawConn, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
		cancel()
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection, err = conn.DialWS(tlsConn, "wss://"+server+path, host, timeout)
		if err != nil {
			_ = tlsConn.Close()
			return nil, "", serverInfo{}, err
		}
		tlsFp, tlsVerify = VerifyState(tlsConn.ConnectionState(), sni)
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

	if err != nil {
		return nil, "", serverInfo{}, err
	}
	if connection == nil {
		return nil, "", serverInfo{}, fmt.Errorf("newConn: unexpected nil connection for tp=%q server=%q", tp, server)
	}

	return handshakeConn(connection, tp, vkey, isTls, tlsVerify, tlsFp, proxyUrl)
}

// HandshakeHooks fix the time and address a handshake sends, for tests that
// record it.
var HandshakeHooks = struct {
	Timestamp  func() int64
	OutboundIP func() net.IP
	PadLen     func() int
}{
	Timestamp:  func() int64 { return common.TimeNow().Unix() - int64(rand.Intn(6)) },
	OutboundIP: common.GetOutboundIP,
	PadLen:     func() int { return rand.Intn(MaxPad) },
}

// handshakeConn verifies connection, which reached the server through tp.
func handshakeConn(connection net.Conn, tp, vkey string, isTls, tlsVerify bool, tlsFp []byte, proxyUrl string) (*conn.Conn, string, serverInfo, error) {
	var info serverInfo
	var err error
	_ = connection.SetDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = connection.SetDeadline(time.Time{}) }()

	c := conn.NewConn(connection)
	res, err := legacy.ClientHandshake(c, legacy.ClientConfig{
		Index:         Ver,
		ClientVersion: version.VERSION,
		Key:           vkey,
		Transport:     tp,
		LocalIP:       HandshakeHooks.OutboundIP(),
		Timestamp:     HandshakeHooks.Timestamp,
		PadLen:        HandshakeHooks.PadLen,
		VerifyFingerprint: func(fp []byte) error {
			if !SkipTLSVerify && isTls && !tlsVerify && !bytes.Equal(fp, tlsFp) {
				logs.Warn("Certificate verification failed. To skip verification, please set -skip_verify=true")
				return errors.New("validation cert incorrect")
			}
			crypt.AddTrustedCert(vkey, fp)
			return nil
		},
	})
	if err != nil {
		_ = c.Close()
		switch {
		case errors.Is(err, legacy.ErrUnauthorized):
			logs.Error("error reading server response: %v", err)
			return nil, "", serverInfo{}, fmt.Errorf("validation key %s incorrect", vkey)
		case errors.Is(err, legacy.ErrVersionMismatch):
			logs.Warn("The client does not match the server version. The current core version of the client is %s", legacy.CoreVersion(Ver))
		}
		return nil, "", serverInfo{}, err
	}
	if res.VersionWarning {
		logs.Warn("The client does not match the server version. The current core version of the client is %s", legacy.CoreVersion(Ver))
	}
	uuid := res.ServerUUID
	info.fingerprint = res.Fingerprint
	if proxyUrl == "" && tp != common.ConnWS && tp != common.ConnWSS {
		if ap, perr := netip.ParseAddrPort(connection.RemoteAddr().String()); perr == nil {
			info.ip = ap.Addr().Unmap().String()
		}
	}
	return c, uuid, info, err
}

// clientCaps returns what this client speaks.
func clientCaps() legacy.Caps { return legacy.CapsOf(Ver, version.VERSION) }

func SendType(c *conn.Conn, connType, uuid string) error {
	if c == nil {
		return fmt.Errorf("sendType: nil conn (connType=%s uuid=%s)", connType, uuid)
	}
	_ = c.SetWriteDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = c.SetWriteDeadline(time.Time{}) }()
	if err := legacy.WriteWork(c, clientCaps(), connType, uuid); err != nil {
		_ = c.Close()
		return err
	}
	c.SetAlive()
	return nil
}

// GetProxyConn dials server directly, through proxyUrl, or through the proxy
// configured in ALL_PROXY and NO_PROXY. proxyUrl may use the http, https,
// socks5, socks5h, socks4 and socks4a schemes, or be a bare host:port, which
// is taken as an http proxy. The whole dial, including any proxy handshake,
// is bounded by timeout.
func GetProxyConn(proxyUrl, server string, timeout time.Duration, localIP string) (net.Conn, error) {
	forward := &net.Dialer{Timeout: timeout, LocalAddr: common.BuildTCPBindAddr(localIP)}
	var d netx.ContextDialer
	if proxyUrl == "" {
		var err error
		if d, err = proxydial.FromEnvironment(forward, nil); err != nil {
			return nil, err
		}
	} else {
		u, err := proxydial.ParseURL(proxyUrl)
		if err != nil {
			return nil, err
		}
		if d, err = proxydial.FromURL(u, forward, nil); err != nil {
			return nil, err
		}
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return d.DialContext(ctx, "tcp", server)
}

// get a basic auth string
//func getBasicAuth(username, password string) string {
//	auth := username + ":" + password
//	return base64.StdEncoding.EncodeToString([]byte(auth))
//}
