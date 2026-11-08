package bridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/coreproto/coreapi"
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
				if task.IsVirtualBackend() {
					continue // listens on nothing
				}
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
		"\nconn_type=tcp\nnew_protocol=true\nauto_reconnection=true\n"+
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
		"\nconn_type=tcp\nnew_protocol=true\nauto_reconnection=true\n"+
		"[files]\nmode=file\nserver_port="+strconv.Itoa(port)+"\nlocal_path="+dir+"\nstrip_pre=/\n")
	runClient(t, func(ctx context.Context) error { return client.StartFromFile(ctx, path) })
	waitStored(t, f, 2, 0)
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

	// Another tunnel with the backend's identifier as its target.
	var backend *model.Tunnel
	f.db.JsonDb.Tasks.Range(func(_, v any) bool {
		if tl := v.(*model.Tunnel); tl.IsVirtualBackend() {
			backend = tl
		}
		return true
	})
	if backend == nil || backend.Port != 0 || backend.Target.TargetStr != model.ServiceTarget(model.FileServiceKind, "port-"+strconv.Itoa(port)) {
		t.Fatalf("backend %+v", backend)
	}
	// A short reconnect uploads again: nothing doubles, the port still works.
	f.a.Kick(f.client.Id, false)
	time.Sleep(300 * time.Millisecond)
	waitStored(t, f, 2, 0)
	get(url)
	other := freeLocalPort(t)
	tl := &model.Tunnel{Mode: "tcp", Port: other, Status: true, Client: f.client, Flow: new(model.Flow),
		Target: &model.Target{TargetStr: backend.Target.TargetStr}, Id: f.db.NextTunnelID()}
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
