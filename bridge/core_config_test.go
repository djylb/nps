package bridge

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/model"
)

// coreConfigEnv is a server with a visitor client (the one with a tunnel to an
// echo) and a provider client that has a secret tunnel to the same echo.
type coreConfigEnv struct {
	f      *coreFixture
	echo   string
	tunnel string // the visitor client's tunnel port
	secret string // password of the secret tunnel
	prov   string // vkey of the provider
}

func newCoreConfigEnv(t *testing.T) *coreConfigEnv {
	t.Helper()
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	e := &coreConfigEnv{echo: echoListener(t), secret: "pw-" + t.Name(), prov: "prov-vkey"}
	e.f = newCoreFixtureWith(t, 0, model.Config{}, session.Options{}, e.prov)
	task := &model.Tunnel{Mode: "secret", Password: e.secret, Status: true, Client: e.f.extra[0], Flow: &model.Flow{},
		TargetType: model.TargetAll, Target: &model.Target{TargetStr: e.echo}}
	if err := e.f.db.NewTask(task); err != nil {
		t.Fatal(err)
	}
	e.tunnel, _ = e.f.startTunnel(t, e.echo, "")
	return e
}

// runClient runs fn until the test ends and waits for it then.
func runClient(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := fn(ctx); err != nil {
			t.Errorf("client: %v", err)
		}
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// waitEcho retries until the address echoes: the clients connect on their own.
func waitEcho(t *testing.T, what, addr string) {
	t.Helper()
	var err error
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if err = echoOnce(addr, "hello "+what); err == nil {
			return
		}
	}
	t.Fatalf("%s: %v", what, err)
}

func echoOnce(addr, msg string) error {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err = readFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("echo %q", buf)
	}
	return nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func writeConf(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// npc.conf files on the new protocol: a provider, a host of the visitor
// client and a file with a relay and an auto local section of the same client
// run in one process and share its session.
func TestConfigFilesOnNewProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	e := newCoreConfigEnv(t)
	common := func(vkey string) string {
		return "[common]\nserver_addr=" + e.f.addr + "\nvkey=" + vkey + "\nconn_type=tcp\nauto_reconnection=false\n"
	}
	relayPort, autoPort := freeLocalPort(t), freeLocalPort(t)
	provider := writeConf(t, "provider.conf", common(e.prov))
	host := writeConf(t, "host.conf", common(e.f.client.VerifyKey))
	visitor := writeConf(t, "visitor.conf", common(e.f.client.VerifyKey)+
		"[secret_relay]\nlocal_type=tcp\nconn_mode=relay\nlocal_ip=127.0.0.1\nlocal_port="+strconv.Itoa(relayPort)+"\npassword="+e.secret+"\n"+
		"[p2p_old]\nlocal_ip=127.0.0.1\nlocal_port="+strconv.Itoa(autoPort)+"\npassword="+e.secret+"\nconn_mode=relay\nlocal_type=tcp\n")
	for _, p := range []string{provider, host, visitor} {
		runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, p) })
	}
	waitEcho(t, "hosted tunnel", e.tunnel)
	waitEcho(t, "relay section", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
	waitEcho(t, "second section", net.JoinHostPort("127.0.0.1", strconv.Itoa(autoPort)))
}

// The same through --launch payloads, with the new protocol turned on the way
// the command line does it.
func TestLaunchOnNewProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	old := config.CommonDefaults
	t.Cleanup(func() { config.CommonDefaults = old })
	e := newCoreConfigEnv(t)
	relayPort := freeLocalPort(t)
	payload := fmt.Sprintf(`[
 {"name":"provider","server":%q,"vkey":%q},
 {"name":"host","server":%q,"vkey":%q},
 {"name":"visitor","common":{"server_addr":%q,"vkey":%q,"auto_reconnection":false},
  "locals":[{"local_type":"tcp","conn_mode":"relay","local_ip":"127.0.0.1","local_port":%d,"password":%q}]}
]`, e.f.addr, e.prov, e.f.addr, e.f.client.VerifyKey, e.f.addr, e.f.client.VerifyKey, relayPort, e.secret)
	profiles, err := launch.Resolve(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	runClient(t, func(ctx context.Context) error {
		return launch.Run(ctx, profiles, map[string]string{}, launch.Options{DisconnectTimeout: 30, P2PTimeout: 5})
	})
	waitEcho(t, "hosted tunnel", e.tunnel)
	waitEcho(t, "relay section", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
}

// A file with only local sections is a pure visitor: it reaches the secret
// tunnel through a session of its own, dialed when the first link is made.
func TestConfigVisitorOnlyOnNewProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	e := newCoreConfigEnv(t)
	port := freeLocalPort(t)
	head := "[common]\nserver_addr=" + e.f.addr + "\nconn_type=tcp\nauto_reconnection=false\nvkey="
	provider := writeConf(t, "provider.conf", head+e.prov+"\n")
	visitor := writeConf(t, "visitor.conf", head+e.f.client.VerifyKey+"\n"+
		"[secret_a]\nlocal_type=tcp\nconn_mode=relay\nlocal_ip=127.0.0.1\nlocal_port="+strconv.Itoa(port)+"\npassword="+e.secret+"\n")
	for _, p := range []string{provider, visitor} {
		runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, p) })
	}
	waitEcho(t, "relay section", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}
