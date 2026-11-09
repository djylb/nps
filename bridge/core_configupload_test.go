package bridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
)

// serveOpened runs the tunnels the bridge hands out, as the server does, and
// takes the hosts.
func serveOpened(t *testing.T, f *coreFixture) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	servers := map[int]proxy.Service{}
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-f.b.OpenHost:
			case task := <-f.b.OpenTask:
				if old := servers[task.Id]; old != nil {
					_ = old.Close()
				}
				srv := proxy.NewTunnelModeServer(proxy.ProcessTunnel, f.b, task, false)
				if err := srv.Bind(); err != nil {
					t.Errorf("bind: %v", err)
					continue
				}
				servers[task.Id] = srv
				wg.Go(func() { _ = srv.Start() })
			}
		}
	})
	t.Cleanup(func() {
		cancel()
		for _, s := range servers {
			_ = s.Close()
		}
		wg.Wait()
	})
}

func countStored(f *coreFixture) (tasks, hosts int) {
	f.db.JsonDb.Tasks.Range(func(_, v any) bool {
		if v.(*model.Tunnel).Client.Id == f.client.Id {
			tasks++
		}
		return true
	})
	f.db.JsonDb.Hosts.Range(func(_, v any) bool { hosts++; return true })
	return
}

func waitStored(t *testing.T, f *coreFixture, tasks, hosts int) {
	t.Helper()
	var gt, gh int
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if gt, gh = countStored(f); gt == tasks && gh == hosts {
			return
		}
	}
	t.Fatalf("stored %d tunnels, %d hosts; want %d, %d", gt, gh, tasks, hosts)
}

// npc.conf on the new protocol registers its tunnels and hosts on the server,
// and registers them again, without duplicates, after the session is lost.
func TestConfigUploadOnNewProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	f := newCoreFixture(t, 0, model.Config{})
	f.client.ConfigConnAllow = true
	serveOpened(t, f)
	echo := echoListener(t)
	port := freeLocalPort(t)
	path := writeConf(t, "npc.conf", "[common]\nserver_addr="+f.addr+"\nvkey="+f.client.VerifyKey+
		"\nconn_type=tcp\nauto_reconnection=true\n"+
		"[echo]\nmode=tcp\ntarget_addr="+echo+"\nserver_port="+strconv.Itoa(port)+"\n"+
		"[web]\nhost=upload.example.com\ntarget_addr="+echo+"\n")
	runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, path) })

	waitStored(t, f, 1, 1)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	waitEcho(t, "uploaded tunnel", addr)

	// The session is lost; the host is deleted meanwhile, so that only a
	// second upload can bring it back.
	f.db.JsonDb.Hosts.Range(func(k, _ any) bool { _ = f.db.DelHost(k.(int)); return true })
	f.a.Kick(f.client.Id, false)
	waitStored(t, f, 1, 1)
	waitEcho(t, "tunnel after reconnect", addr)
	time.Sleep(200 * time.Millisecond)
	if tasks, hosts := countStored(f); tasks != 1 || hosts != 1 {
		t.Fatalf("after reconnect: %d tunnels, %d hosts", tasks, hosts)
	}
}

// A file tunnel of npc.conf is a virtual service of the session: the upload
// creates the tunnel, SERVICE binds it, and visitors reach the local files.
func TestFileTunnelIsVirtualService(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	f := newCoreFixture(t, 0, model.Config{})
	f.client.ConfigConnAllow = true
	serveOpened(t, f)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("file service"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := freeLocalPort(t)
	path := writeConf(t, "npc.conf", "[common]\nserver_addr="+f.addr+"\nvkey="+f.client.VerifyKey+
		"\nconn_type=tcp\nauto_reconnection=true\n"+
		"[files]\nmode=file\nserver_port="+strconv.Itoa(port)+"\nlocal_path="+dir+"\nstrip_pre=/\n")
	runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, path) })
	waitStored(t, f, 1, 0)
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/hello.txt"
	hc := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	get := func(url string) {
		t.Helper()
		var last string
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			r, err := hc.Get(url)
			if err != nil {
				last = err.Error()
				continue
			}
			b, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if string(b) == "file service" {
				return
			}
			last = r.Status
		}
		t.Fatalf("file not served at %s: %s", url, last)
	}
	get(url)

	target := model.ServiceTarget(model.FileServiceKind, "port-"+strconv.Itoa(port))
	listed := func() int {
		n := 0
		for _, v := range f.b.VirtualServices() {
			if v.Target == target && v.Kind == model.FileServiceKind && v.Network == "tcp" {
				n++
			}
		}
		return n
	}
	if listed() != 1 {
		t.Fatalf("listed %+v", f.b.VirtualServices())
	}

	// A short reconnect uploads again: nothing doubles, the port still works.
	f.a.Kick(f.client.Id, false)
	time.Sleep(300 * time.Millisecond)
	waitStored(t, f, 1, 0)
	get(url)
	other := freeLocalPort(t)
	tl := &model.Tunnel{Mode: "tcp", Port: other, Status: true, Client: f.client, Flow: new(model.Flow),
		Target: &model.Target{TargetStr: target}, Id: f.db.NextTunnelID()}
	if err := f.db.NewTask(tl); err != nil {
		t.Fatal(err)
	}
	f.b.OpenTask <- tl
	get("http://127.0.0.1:" + strconv.Itoa(other) + "/hello.txt")
}

// An upload the old protocol would not take is refused on a session too, and
// nothing is registered.
func TestConfigUploadRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*model.Client)
	}{
		{"config connection not allowed", func(c *model.Client) { c.ConfigConnAllow = false }},
		{"visitor key", func(c *model.Client) { c.ConfigConnAllow = true; c.KeyKind = model.KeyVisitor }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoreFixture(t, 0, model.Config{})
			tc.set(f.client)
			s := f.joinCore(t, client.NewCoreExit("", 0))
			up, ok := s.(coreapi.ConfigUploader)
			if !ok {
				t.Fatal("session cannot upload")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if c, err := up.OpenConfigUpload(ctx); err == nil {
				_ = c.Close()
				t.Fatal("upload was admitted")
			}
			if tasks, hosts := countStored(f); tasks+hosts != 0 {
				t.Fatalf("%d tunnels, %d hosts registered", tasks, hosts)
			}
		})
	}
}

// A generated tunnel whose port is in use is left out without a failure: the
// backend is listed and the rest of the file is taken.
func TestFileTunnelPortTaken(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	f := newCoreFixture(t, 0, model.Config{})
	f.client.ConfigConnAllow = true
	serveOpened(t, f)
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	path := writeConf(t, "npc.conf", "[common]\nserver_addr="+f.addr+"\nvkey="+f.client.VerifyKey+
		"\nconn_type=tcp\nauto_reconnection=true\n"+
		"[files]\nmode=file\nserver_port="+strconv.Itoa(port)+"\nlocal_path="+t.TempDir()+"\nstrip_pre=/\n")
	runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, path) })
	for end := time.Now().Add(10 * time.Second); len(f.b.VirtualServices()) != 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("service not listed")
		}
	}
	if tasks, _ := countStored(f); tasks != 0 {
		t.Fatalf("%d tunnels stored", tasks)
	}
}

// A service answers on the network it reported, and only that one.
func TestServiceNetwork(t *testing.T) {
	f := newCoreFixture(t, 0, model.Config{})
	s := f.joinCore(t, client.NewCoreExit("", 0))
	served := make(chan struct{}, 1)
	svc := coreapi.LocalService{Kind: "x", Name: "u", Network: coreapi.NetworkUDP, Serve: func(_ context.Context, c net.Conn) {
		served <- struct{}{}
		_ = c.Close()
	}}
	if err := s.(coreapi.ServiceHost).PublishServices(context.Background(), []coreapi.LocalService{svc}); err != nil {
		t.Fatal(err)
	}
	f.waitConnected(t)
	v, _ := f.b.Client.Load(f.client.Id)
	cs := v.(*Client).GetNode().coreSession()
	id := coreapi.ServiceID("x", "u")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cs.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Service: id, WantReply: true}); err == nil {
		t.Fatal("tcp reached a udp service")
	}
	c, err := cs.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkUDP, Service: id, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-served
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if l := f.b.VirtualServices(); len(l) == 1 && l[0].Kind == "x" && l[0].Network == "udp" {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("listed %+v", f.b.VirtualServices())
		}
	}
	// The report ends with its session and leaves nothing behind.
	_ = s.Close()
	<-s.Done()
	for end := time.Now().Add(3 * time.Second); len(f.b.VirtualServices()) != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("still listed after the session ended")
		}
	}
	if tasks, _ := countStored(f); tasks != 0 {
		t.Fatalf("%d tunnels stored", tasks)
	}
}

// An upload with the public key creates a client with a new key, and npc
// goes on with that key, as with the old protocol's config exchange.
func TestConfigUploadPublicKey(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	f := newCoreFixture(t, 0, model.Config{})
	f.db.OnClientChanged(func(c *model.Client, deleted bool) {
		if deleted {
			f.a.ClientRemoved(c.Id)
			return
		}
		f.a.ClientChanged(coreapi.ClientRecord{ID: c.Id, VKey: c.VerifyKey, Enabled: c.Status, Crypt: c.Cnf.Crypt,
			ConfigOnly: c.KeyKind == model.KeyPublic})
	})
	pub := model.NewClient("public-key", true, true)
	pub.KeyKind, pub.Status = model.KeyPublic, true
	if err := f.db.NewClient(pub); err != nil {
		t.Fatal(err)
	}
	serveOpened(t, f)
	echo := echoListener(t)
	port := freeLocalPort(t)
	path := writeConf(t, "npc.conf", "[common]\nserver_addr="+f.addr+"\nvkey=public-key\nconn_type=tcp\nauto_reconnection=true\ncompress=true\n"+
		"[echo]\nmode=tcp\ntarget_addr="+echo+"\nserver_port="+strconv.Itoa(port)+"\n")
	runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, path) })
	waitEcho(t, "tunnel of the issued key", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if v, ok := f.b.Client.Load(pub.Id); ok && v.(*Client).NodeCount() != 0 {
		t.Fatal("the public key became a node")
	}
	// [common] compress is the issued client's default compression (§7.1).
	var compressed bool
	f.db.JsonDb.Clients.Range(func(_, v any) bool {
		if c := v.(*model.Client); c.NoStore && c.KeyKind == model.KeyControl {
			compressed = len(c.Cnf.CompressList()) == 1 && c.Cnf.CompressList()[0] == "snappy"
		}
		return true
	})
	if !compressed {
		t.Fatal("[common] compress not mapped to the client's default")
	}
}

// npc register on the new protocol registers the session's address, under the
// key rule of WorkRegister.
func TestRegisterOnNewProtocol(t *testing.T) {
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	connect.Install()
	t.Cleanup(func() { client.CoreOuter = nil })
	f := newCoreFixture(t, 0, model.Config{})
	cfg := config.NewCommonConfig()
	cfg.Server, cfg.VKey, cfg.Tp = f.addr, f.client.VerifyKey, "tcp"
	if err := client.RegisterLocalIP(cfg, 2); err != nil {
		t.Fatal(err)
	}
	if !ipreg.Registered(f.client.Id, netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("address not registered")
	}
	ipreg.Reset()
	f.client.KeyKind = model.KeyVisitor
	if err := client.RegisterLocalIP(cfg, 2); err == nil {
		t.Fatal("a visitor key registered")
	}
	if ipreg.Count() != 0 {
		t.Fatal("refused registration stored")
	}
}

// npc status lists what runs only while another npc of the key is connected:
// the session asking does not count.
func TestStatusCountsOtherNodes(t *testing.T) {
	f := newCoreFixture(t, 0, model.Config{})
	f.joinCore(t, client.NewCoreExit("", 0))
	uuid := f.b.mustOnlyNode(t, f.client.Id)
	if f.b.hasNodeBesides(f.client.Id, uuid) {
		t.Fatal("its own node counted")
	}
	if !f.b.hasNodeBesides(f.client.Id, "other") {
		t.Fatal("a connected node not counted")
	}
}

func (s *Bridge) mustOnlyNode(t *testing.T, id int) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := s.Client.Load(id); ok {
			if nodes := v.(*Client).nodeList(); len(nodes) == 1 {
				return nodes[0].UUID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no node")
	return ""
}

type serviceSession struct {
	coreapi.Session
	id int
}

func (s serviceSession) ClientID() int { return s.id }

// A restarted npc reports from a new session while the old one is suspended:
// the service is one row.
func TestVirtualServicesOneEntryPerService(t *testing.T) {
	f := newCoreFixture(t, 0, model.Config{})
	a := []coreapi.ServiceInfo{{Kind: "file", Name: "b", Network: "tcp"}, {Kind: "file", Name: "a", Network: "tcp"}}
	f.b.coreServicesOf.Store(serviceSession{id: 1}, a)
	f.b.coreServicesOf.Store(&serviceSession{id: 1}, a)
	f.b.coreServicesOf.Store(&serviceSession{id: 2}, a[:1])
	got := f.b.VirtualServices()
	if len(got) != 3 || got[0].Client != 1 || got[0].Name != "a" || got[1].Name != "b" || got[2].Client != 2 {
		t.Fatalf("%+v", got)
	}
}
