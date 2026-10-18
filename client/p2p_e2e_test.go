package client

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// TestP2PEndToEnd runs a bridge with its P2P service and two clients on
// loopback: the provider registers, the visitor punches a direct path
// through the bridge, connects QUIC over it and carries a TCP connection to
// a target behind the provider, with the relay disabled.
func TestP2PEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	env := startP2PTestEnv(t)
	target := serveTCP(t, func(c net.Conn) {
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	})
	// Two devices run the same visitor command at once: each gets its own
	// direct path to the provider.
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { runVisitor(t, env.ctx, env.cancel, env.bridgeAddr, p2pTestVisitorKey, p2pTestPassword, target) })
	}
	wg.Wait()
}

// TestP2PDeclinedByProvider checks that a visitor whose provider has P2P
// disabled fails with the provider's answer and backs off as from a
// configuration problem.
func TestP2PDeclinedByProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	old := DisableP2P
	DisableP2P = true
	t.Cleanup(func() { DisableP2P = old })
	env := startP2PTestEnv(t)
	mgr := NewP2PManager(env.ctx, env.cancel, &config.CommonConfig{
		Server: env.bridgeAddr, VKey: p2pTestVisitorKey, Tp: "tcp", DisconnectTime: 30,
		Client: &file.Client{Cnf: &file.Config{}},
	})
	t.Cleanup(mgr.Close)
	ctx, cancel := context.WithTimeout(env.ctx, 30*time.Second)
	defer cancel()
	tr, err := newP2PPeer(mgr, p2pTestPassword, "test").punch(ctx, false)
	if err == nil {
		tr.Close(p2pCodeClosed, "closed")
		t.Fatal("punched a direct path to a provider with P2P disabled")
	}
	if class, _, _ := classifyP2PError(err); class != p2pBackoffFatal || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("punch = %v (backoff class %d), want the provider's refusal", err, class)
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
}

// startP2PTestEnv runs a bridge with its P2P service on loopback, a
// provider client registered on it and the p2p task of p2pTestPassword.
func startP2PTestEnv(t *testing.T) p2pTestEnv {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDb := file.Db
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	t.Cleanup(func() { file.Db = oldDb })

	provider := file.NewClient(p2pTestProviderKey, true, false)
	visitor := file.NewClient(p2pTestVisitorKey, true, false)
	for _, c := range []*file.Client{provider, visitor} {
		if err := file.GetDb().NewClient(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.GetDb().NewTask(&file.Tunnel{Id: 1, Mode: "p2p", Password: p2pTestPassword, Status: true, Client: provider, Target: &file.Target{}}); err != nil {
		t.Fatal(err)
	}

	b := bridge.NewTunnel(&sync.Map{}, 30)
	if err := b.StartP2P(bridge.P2PConfig{IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.StopP2P)
	bridgeAddr := serveTCP(t, func(c net.Conn) { b.CliProcess(conn.NewConn(c), "tcp") })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rp := NewRPClient(bridgeAddr, p2pTestProviderKey, "tcp", "", "", "", nil, 30, nil)
	rpDone := make(chan struct{})
	go func() {
		defer close(rpDone)
		rp.Start(ctx)
	}()
	t.Cleanup(func() {
		rp.Close()
		<-rpDone
	})
	waitFor(t, 10*time.Second, "provider registration", func() bool {
		v, ok := b.Client.Load(provider.Id)
		return ok && v.(*bridge.Client).GetNode() != nil && v.(*bridge.Client).GetNode().GetSignal() != nil
	})
	return p2pTestEnv{ctx: ctx, cancel: cancel, bridgeAddr: bridgeAddr}
}

// runVisitor starts a visitor with the relay disabled, waits for its direct
// path and carries a connection to target over it.
func runVisitor(t *testing.T, ctx context.Context, cancel context.CancelFunc, bridgeAddr, vkey, password, target string) {
	mgr := NewP2PManager(ctx, cancel, &config.CommonConfig{
		Server: bridgeAddr, VKey: vkey, Tp: "tcp", DisconnectTime: 30,
		Client: &file.Client{Cnf: &file.Config{}},
	})
	t.Cleanup(mgr.Close)
	// A free port may be taken by another test before the local server
	// binds it: try another one then.
	var port int
	for attempt := 0; ; attempt++ {
		port = freePort(t)
		err := mgr.StartLocalServer(&config.LocalServer{Type: "p2p", Port: port, Password: password, Target: target, TargetType: "tcp"})
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EADDRINUSE) || attempt == 4 {
			t.Error(err)
			return
		}
	}
	mgr.mu.Lock()
	peer := mgr.peers[password]
	mgr.mu.Unlock()
	wctx, wcancel := context.WithTimeout(ctx, 60*time.Second)
	defer wcancel()
	if peer.waitActive(wctx) == nil {
		t.Error("no direct path within 60s")
		return
	}

	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = c.Close() }()
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _, _ = c.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Errorf("echo over the direct path: %v", err)
		return
	}
	for i := range got {
		if got[i] != payload[i] {
			t.Errorf("echo differs at byte %d", i)
			return
		}
	}
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
