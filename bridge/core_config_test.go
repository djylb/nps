package bridge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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
	tunnel string        // the visitor client's tunnel port
	secret string        // password of the secret tunnel
	prov   string        // vkey of the provider
	task   *model.Tunnel // the secret tunnel
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
	e.task = task
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
	profiles, err := launch.Resolve(context.Background(), "@"+writeConf(t, "launch.json", payload))
	if err != nil {
		t.Fatal(err)
	}
	runClient(t, func(ctx context.Context) error { return runProfiles(ctx, profiles) })
	waitEcho(t, "hosted tunnel", e.tunnel)
	waitEcho(t, "relay section", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
}

// runProfiles runs each profile until ctx ends.
func runProfiles(ctx context.Context, profiles []launch.Profile) error {
	var wg sync.WaitGroup
	for _, p := range profiles {
		wg.Go(func() {
			_, _ = p.Run(ctx, "", launch.Options{DisconnectTimeout: 30, P2PTimeout: 5})
		})
	}
	wg.Wait()
	return nil
}

// composedProfiles is what npc runs for these -launch and -L values.
func composedProfiles(t *testing.T, links, accesses []string) []launch.Profile {
	t.Helper()
	locals, err := launch.ParseLocals(accesses)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := launch.NewComposer(launch.LaunchSources(links), locals).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan.Profiles
}

// The same through launch links and -L, as the web command and the command
// line write them: the provider and the host on one npc, a visitor on another.
func TestLaunchLinksOnNewProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	old := config.CommonDefaults
	t.Cleanup(func() { config.CommonDefaults = old })
	e := newCoreConfigEnv(t)
	relayPort := freeLocalPort(t)
	link := func(vkey, name string) string {
		return "npc://" + url.PathEscape(vkey) + "@" + e.f.addr + "?type=tcp#" + name
	}
	hosting := composedProfiles(t, []string{link(e.prov, "provider"), link(e.f.client.VerifyKey, "host")}, nil)
	visiting := composedProfiles(t, []string{link(e.f.client.VerifyKey, "home")},
		[]string{"tcp://127.0.0.1:" + strconv.Itoa(relayPort) + "?conn_mode=relay&via=home&access=" + url.QueryEscape(e.secret)})
	if visiting[0].Direct || len(visiting[0].Config.LocalServer) != 1 {
		t.Fatalf("%+v", visiting[0])
	}
	runClient(t, func(ctx context.Context) error { return runProfiles(ctx, append(hosting, visiting...)) })
	waitEcho(t, "hosted tunnel", e.tunnel)
	waitEcho(t, "relay section", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
}

// A visitor names any destination on a tunnel without a list; naming none
// on a tunnel without a default target fails.
func TestLaunchAccessTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	e := newCoreConfigEnv(t)
	e.task.Target = &model.Target{}
	named, none := freeLocalPort(t), freeLocalPort(t)
	access := "&conn_mode=relay&access=" + url.QueryEscape(e.secret)
	hosting := composedProfiles(t, []string{"npc://" + e.prov + "@" + e.f.addr + "?type=tcp"}, nil)
	visiting := composedProfiles(t, []string{"npc://" + url.PathEscape(e.f.client.VerifyKey) + "@" + e.f.addr + "?type=tcp"}, []string{
		"tcp://127.0.0.1:" + strconv.Itoa(named) + "?target=" + e.echo + access,
		"tcp://127.0.0.1:" + strconv.Itoa(none) + "?" + access[1:],
	})
	runClient(t, func(ctx context.Context) error { return runProfiles(ctx, append(hosting, visiting...)) })
	waitEcho(t, "named destination", net.JoinHostPort("127.0.0.1", strconv.Itoa(named)))
	if err := echoOnce(net.JoinHostPort("127.0.0.1", strconv.Itoa(none)), "x"); err == nil {
		t.Fatal("a link naming nothing reached something on a tunnel without a target")
	}
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

// socks5Connect asks the proxy at addr for dst, an IPv4 host:port, and returns
// the reply code with the connection, closed unless it succeeded.
func socks5Connect(t *testing.T, addr, dst string) (byte, net.Conn) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return 0xff, nil
	}
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	ap := netip.MustParseAddrPort(dst)
	req := append([]byte{5, 1, 0, 1}, ap.Addr().AsSlice()...)
	req = append(req, byte(ap.Port()>>8), byte(ap.Port()))
	head := make([]byte, 2)
	if _, err = c.Write([]byte{5, 1, 0}); err == nil {
		_, err = io.ReadFull(c, head)
	}
	reply := make([]byte, 10)
	if err == nil {
		if _, err = c.Write(req); err == nil {
			_, err = io.ReadFull(c, reply)
		}
	}
	if err != nil {
		_ = c.Close()
		return 0xff, nil
	}
	if reply[1] != 0 {
		_ = c.Close()
		return reply[1], nil
	}
	return 0, c
}

// httpConnect asks the proxy at addr to CONNECT dst and returns the status
// with the connection, closed unless it succeeded.
func httpConnect(t *testing.T, addr, dst string) (int, net.Conn) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	_, _ = io.WriteString(c, "CONNECT "+dst+" HTTP/1.1\r\nHost: "+dst+"\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		_ = c.Close()
		t.Fatalf("CONNECT %s: %v", dst, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = c.Close()
		return resp.StatusCode, nil
	}
	return resp.StatusCode, c
}

// A mixed local section answers both proxy protocols only once the exit has
// taken the destination or refused it.
func TestMixedVisitorRepliesAfterExit(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	e := newCoreConfigEnv(t)
	e.task.DestAclMode, e.task.DestAclRules = model.AclBlacklist, "10.1.2.3"
	e.task.CompileDestACL()
	port := freeLocalPort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	head := "[common]\nserver_addr=" + e.f.addr + "\nconn_type=tcp\nauto_reconnection=false\nvkey="
	provider := writeConf(t, "provider.conf", head+e.prov+"\n")
	visitor := writeConf(t, "visitor.conf", head+e.f.client.VerifyKey+"\n"+
		"[secret_a]\nlocal_type=mixed\nconn_mode=relay\nlocal_ip=127.0.0.1\nlocal_port="+strconv.Itoa(port)+"\npassword="+e.secret+"\n")
	for _, p := range []string{provider, visitor} {
		runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, p) })
	}
	echoed := func(c net.Conn, msg string) {
		t.Helper()
		defer func() { _ = c.Close() }()
		if _, err := io.WriteString(c, msg); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, len(msg))
		if _, err := io.ReadFull(c, b); err != nil || string(b) != msg {
			t.Fatalf("echo %q %v", b, err)
		}
	}
	var c net.Conn
	var rep byte
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if rep, c = socks5Connect(t, addr, e.echo); rep == 0 {
			break
		}
	}
	if rep != 0 {
		t.Fatalf("socks5 connect to the target: reply %d", rep)
	}
	echoed(c, "through socks5")
	status, c := httpConnect(t, addr, e.echo)
	if status != http.StatusOK {
		t.Fatalf("CONNECT to the target: %d", status)
	}
	echoed(c, "through connect")

	// outside the tunnel's destination list: the provider's policy refuses
	const denied = "10.1.2.3:4567"
	if rep, _ := socks5Connect(t, addr, denied); rep != 2 {
		t.Fatalf("denied destination: socks5 reply %d, want 2", rep)
	}
	if status, _ := httpConnect(t, addr, denied); status != http.StatusForbidden {
		t.Fatalf("denied destination: CONNECT %d, want 403", status)
	}
}
