package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/servercfg"
)

// TestP2PEndToEnd runs a bridge with its P2P service and two clients on
// loopback: the provider registers, the visitor punches a direct path
// through the bridge, connects QUIC over it and carries a TCP connection to
// the task's target behind the provider, in direct mode (no relay).
func TestP2PEndToEnd(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("secure=%t", secure), func(t *testing.T) {
			testP2PEndToEnd(t, secure)
		})
	}
}

func testP2PEndToEnd(t *testing.T, secure bool) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	env := startP2PTestEnv(t, secure, serveTCP(t, echoConn))
	// Two devices run the same visitor command at once: each gets its own
	// direct path to the provider.
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			addr, link := env.visitor(t, &config.LocalServer{Type: config.LocalTCP, ConnMode: config.ConnDirect, Ip: "127.0.0.1"})
			waitDirect(t, env.ctx, link)
			checkTCPEcho(t, addr, 256<<10)
		})
	}
	wg.Wait()
	if n := env.relayed.Load(); n != 0 {
		t.Fatalf("direct mode relayed %d links", n)
	}
}

// Every local type reaches the task's target over the relay and over the
// direct path.
func TestSecretLocalTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	tcpTarget := serveTCP(t, echoConn)
	udpTarget := serveUDPEcho(t)
	for _, mode := range []string{config.ConnRelay, config.ConnDirect} {
		for _, typ := range []string{config.LocalTCP, config.LocalUDP, config.LocalTCPUDP, config.LocalMixed} {
			t.Run(mode+"/"+typ, func(t *testing.T) {
				target := tcpTarget
				if typ != config.LocalTCP && typ != config.LocalMixed {
					target = tcpTarget + "\n" + udpTarget
				}
				env := startP2PTestEnv(t, false, target)
				if typ == config.LocalUDP {
					env.setTarget(udpTarget)
				}
				addr, link := env.visitor(t, &config.LocalServer{Type: typ, ConnMode: mode, Ip: "127.0.0.1"})
				if mode == config.ConnDirect {
					waitDirect(t, env.ctx, link)
				}
				switch typ {
				case config.LocalTCP:
					checkTCPEcho(t, addr, 4096)
				case config.LocalUDP:
					checkUDPEcho(t, addr)
				case config.LocalTCPUDP:
					env.setTarget(tcpTarget)
					checkTCPEcho(t, addr, 4096)
					env.setTarget(udpTarget)
					checkUDPEcho(t, addr)
				case config.LocalMixed:
					checkSocks5Echo(t, addr, tcpTarget)
				}
				relayed := env.relayed.Load()
				if mode == config.ConnRelay && relayed == 0 || mode == config.ConnDirect && relayed != 0 {
					t.Fatalf("%s mode relayed %d links", mode, relayed)
				}
			})
		}
	}
}

// In auto mode links are relayed until the direct path is up, go direct
// then, are relayed again when it is lost and go direct once it is back.
func TestSecretAutoSwitchesPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	env := startP2PTestEnv(t, false, serveTCP(t, echoConn))
	addr, link := env.visitor(t, &config.LocalServer{Type: config.LocalTCP, ConnMode: config.ConnAuto, Ip: "127.0.0.1"})
	d := link.directPath()
	if d.active() == nil {
		checkTCPEcho(t, addr, 1024)
		if env.relayed.Load() == 0 {
			t.Fatal("link before the direct path was not relayed")
		}
	}
	tr := waitDirect(t, env.ctx, link)
	before := env.relayed.Load()
	checkTCPEcho(t, addr, 1024)
	if env.relayed.Load() != before {
		t.Fatal("link relayed while the direct path is up")
	}
	_ = tr.Close()
	waitFor(t, 5*time.Second, "direct path taken out of use", func() bool { return d.active() != tr })
	if d.active() == nil {
		checkTCPEcho(t, addr, 1024)
		if env.relayed.Load() == before {
			t.Fatal("link not relayed while the direct path is down")
		}
	}
	if waitDirect(t, env.ctx, link) == tr {
		t.Fatal("closed transport in use")
	}
	before = env.relayed.Load()
	checkTCPEcho(t, addr, 1024)
	if env.relayed.Load() != before {
		t.Fatal("link relayed after the direct path came back")
	}
}

// The visitor reaches only what the task allows, on both paths: without
// allow_secret_link its own target is replaced by the task's, with it a
// destination the task's list denies is refused.
func TestSecretDirectPathPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	allowed := serveTCP(t, func(c net.Conn) { defer func() { _ = c.Close() }(); _, _ = io.WriteString(c, "task") })
	other := serveTCP(t, func(c net.Conn) { defer func() { _ = c.Close() }(); _, _ = io.WriteString(c, "other") })
	for _, allowLink := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow_secret_link=%t", allowLink), func(t *testing.T) {
			useServerConfig(t, fmt.Sprintf("allow_secret_link=%t\n", allowLink))
			env := startP2PTestEnv(t, false, allowed)
			env.task.DestAclMode, env.task.DestAclRules = model.AclBlacklist, other
			for _, mode := range []string{config.ConnRelay, config.ConnDirect} {
				addr, link := env.visitor(t, &config.LocalServer{Type: config.LocalTCP, ConnMode: mode, Ip: "127.0.0.1", Target: other})
				if mode == config.ConnDirect {
					waitDirect(t, env.ctx, link)
				}
				got := readAll(t, addr)
				switch {
				case !allowLink && got != "task":
					t.Fatalf("%s: visitor reached %q, want the task's target", mode, got)
				case allowLink && got != "":
					t.Fatalf("%s: visitor reached the denied destination (%q)", mode, got)
				}
			}
		})
	}
}

// TestP2PDeclinedByProvider checks that a visitor whose provider cannot
// apply the task's policy fails with the provider's answer and backs off as
// from a configuration problem.
func TestP2PDeclinedByProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	env := startP2PTestEnv(t, false, "127.0.0.1:1")
	// A rule npc cannot apply without geodata.
	env.task.DestAclMode, env.task.DestAclRules = model.AclWhitelist, "geosite:cn"
	_, link := env.visitor(t, &config.LocalServer{Type: config.LocalTCP, ConnMode: config.ConnRelay, Ip: "127.0.0.1"})
	ctx, cancel := context.WithTimeout(env.ctx, 30*time.Second)
	defer cancel()
	tr, err := newDirectPath(link).punch(ctx, false)
	if err == nil {
		_ = tr.Close()
		t.Fatal("punched a direct path to a provider that cannot apply the policy")
	}
	var b p2pBackoff
	if a := b.failed(err).Action; a != holepunch.ActionGiveUp || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("punch = %v (next %s), want the provider's refusal", err, a)
	}
}

const (
	p2pTestProviderKey = "e2e-provider-key"
	p2pTestVisitorKey  = "e2e-visitor-key"
	p2pTestPassword    = "e2e-password"
)

type p2pTestEnv struct {
	ctx        context.Context
	cancel     context.CancelFunc
	bridgeAddr string
	task       *model.Tunnel
	relayed    *atomic.Int32 // links nps relayed
}

// setTarget replaces the task's target.
func (env p2pTestEnv) setTarget(target string) {
	env.task.Target.TargetStr = target
}

// startP2PTestEnv runs a bridge with its P2P service on loopback, the relay
// of the server, a provider client registered on it and the secret task of
// p2pTestPassword reaching target; secure runs the bridge in secure mode.
func startP2PTestEnv(t *testing.T, secure bool, target string) p2pTestEnv {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)

	provider := model.NewClient(p2pTestProviderKey, true, false)
	visitor := model.NewClient(p2pTestVisitorKey, true, false)
	for _, c := range []*model.Client{provider, visitor} {
		if err := db.NewClient(c); err != nil {
			t.Fatal(err)
		}
	}
	task := &model.Tunnel{Id: 1, Mode: "secret", Password: p2pTestPassword, Status: true, Client: provider, Flow: &model.Flow{},
		TargetType: model.TargetAll, Target: &model.Target{TargetStr: target}}
	if err := db.NewTask(task); err != nil {
		t.Fatal(err)
	}

	id, err := crypt.NewIdentity(tls.Certificate{})
	if err != nil {
		t.Fatal(err)
	}
	b := bridge.NewTunnel(db, &sync.Map{}, bridge.Options{DisconnectTime: 30, SecureMode: secure, Cert: id.Cert})
	if err := b.StartP2P(bridge.P2PConfig{IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.StopP2P)
	bridgeAddr := serveTCP(t, func(c net.Conn) { b.CliProcess(conn.NewConn(c), "tcp") })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The relay, as the server runs it (Server.handleSecret).
	relayed := new(atomic.Int32)
	go func() {
		for {
			select {
			case sc := <-b.SecretChan:
				tk := db.GetTaskByMd5Password(sc.Password)
				if tk == nil {
					_ = sc.Conn.Close()
					continue
				}
				relayed.Add(1)
				f := servercfg.Current().Feature
				go func() {
					_ = proxy.NewSecretServer(b, tk, f.AllowLocalProxy, f.AllowSecretLink, f.AllowSecretLocal).HandleSecret(sc.Conn, sc.Wire)
				}()
			case <-ctx.Done():
				return
			}
		}
	}()
	rp := NewRPClient(bridgeAddr, p2pTestProviderKey, "tcp", "", "", "", nil, 30, nil)
	rpDone := make(chan struct{})
	go func() {
		defer close(rpDone)
		rp.Start(ctx)
	}()
	t.Cleanup(func() {
		rp.Close()
		<-rpDone
		// The bridge reads the store until the signal connection has ended.
		waitFor(t, 10*time.Second, "signal connection ended", func() bool {
			v, ok := b.Client.Load(provider.Id)
			if !ok {
				return true
			}
			n, ok := v.(*bridge.Client).GetNodeByUUID(rp.uuid)
			return !ok || n.GetSignal() == nil
		})
	})
	waitFor(t, 10*time.Second, "provider registration", func() bool {
		v, ok := b.Client.Load(provider.Id)
		return ok && v.(*bridge.Client).GetNode() != nil && v.(*bridge.Client).GetNode().GetSignal() != nil
	})
	return p2pTestEnv{ctx: ctx, cancel: cancel, bridgeAddr: bridgeAddr, task: task, relayed: relayed}
}

// visitor starts a visitor manager with section l of the test task on a
// free port and returns the port's address and the section's peer link.
func (env p2pTestEnv) visitor(t *testing.T, l *config.LocalServer) (string, *peerLink) {
	t.Helper()
	mgr := NewSecretManager(env.ctx, env.cancel, &config.CommonConfig{
		Server: env.bridgeAddr, VKey: p2pTestVisitorKey, Tp: "tcp", DisconnectTime: 30,
		Client: &model.Client{Cnf: &model.Config{}},
	})
	t.Cleanup(mgr.Close)
	l.Password = p2pTestPassword
	// A free port may be taken by another test before the local server
	// binds it: try another one then.
	for attempt := 0; ; attempt++ {
		l.Port = freePort(t)
		err := mgr.StartLocalServer(l)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EADDRINUSE) || attempt == 4 {
			t.Fatal(err)
		}
	}
	mgr.mu.Lock()
	link := mgr.links[p2pTestPassword]
	mgr.mu.Unlock()
	return net.JoinHostPort(l.Ip, strconv.Itoa(l.Port)), link
}

// waitDirect waits for the link's direct path and returns its transport.
func waitDirect(t *testing.T, ctx context.Context, link *peerLink) *p2pTransport {
	t.Helper()
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tr := link.directPath().waitActive(wctx)
	if tr == nil {
		t.Fatal("no direct path within 60s")
	}
	return tr
}

func echoConn(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = io.Copy(c, c)
}

func checkTCPEcho(t *testing.T, addr string, size int) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _, _ = c.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("tcp echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("tcp echo differs")
	}
}

func serveUDPEcho(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

func checkUDPEcho(t *testing.T, addr string) {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	buf := make([]byte, 64)
	// The first datagrams may go out before the link is open: retry.
	for range 20 {
		_, _ = c.Write([]byte("datagram"))
		_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if n, err := c.Read(buf); err == nil {
			if string(buf[:n]) != "datagram" {
				t.Fatalf("udp echo = %q", buf[:n])
			}
			return
		}
	}
	t.Fatal("no udp echo")
}

func checkSocks5Echo(t *testing.T, addr, target string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = c.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil || reply[1] != 0 {
		t.Fatalf("socks5 method reply %v, %v", reply, err)
	}
	ap, err := netip.ParseAddrPort(target)
	if err != nil {
		t.Fatal(err)
	}
	ip := ap.Addr().As4()
	req := append([]byte{5, 1, 0, 1}, ip[:]...)
	req = append(req, byte(ap.Port()>>8), byte(ap.Port()))
	_, _ = c.Write(req)
	connect := make([]byte, 10)
	if _, err := io.ReadFull(c, connect); err != nil || connect[1] != 0 {
		t.Fatalf("socks5 connect reply %v, %v", connect, err)
	}
	assertEcho(t, c, "socks")
}

// readAll returns what a connection to addr reads until it ends.
func readAll(t *testing.T, addr string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	b, _ := io.ReadAll(c)
	return string(b)
}

// useServerConfig makes conf the server configuration for the test.
func useServerConfig(t *testing.T, conf string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nps.conf")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := servercfg.Load(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		empty := filepath.Join(dir, "empty.conf")
		_ = os.WriteFile(empty, nil, 0o600)
		_ = servercfg.Load(empty)
	})
}

// serveTCP serves every connection of a new loopback listener with handle
// and returns its address.
func serveTCP(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
	return ln.Addr().String()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
