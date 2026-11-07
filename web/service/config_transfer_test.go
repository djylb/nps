package service

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/webhook"
	"github.com/djylb/nps/server"
)

func useTempDb(t *testing.T) {
	t.Helper()
	oldHosts, oldKeys, oldPasswords := file.HostIndex, file.Blake2bVkeyIndex, file.TaskPasswordIndex
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(file.ReplaceDb(&file.DbUtils{JsonDb: file.NewJsonDb(root)}))
	file.GetDb().JsonDb.Global = &model.Glob{}
	file.HostIndex = index.NewDomainIndex()
	file.Blake2bVkeyIndex = index.NewStringIDIndex()
	file.TaskPasswordIndex = index.NewStringIDIndex()
	t.Cleanup(func() {
		file.HostIndex, file.Blake2bVkeyIndex, file.TaskPasswordIndex = oldHosts, oldKeys, oldPasswords
	})
}

func seedConfig(t *testing.T) (DefaultClientService, DefaultIndexService) {
	t.Helper()
	backend := DefaultBackend()
	clients := DefaultClientService{ConfigProvider: servercfg.Current, Backend: backend}
	indexes := DefaultIndexService{Backend: backend}
	id, err := clients.Add(AddClientInput{VKey: "vkey-a", Remark: "a", WebUserName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexes.AddTunnel(AddTunnelInput{ClientID: id, Mode: "secret", Password: "pw-a", Target: "127.0.0.1:80", IsAdmin: true, Remark: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := indexes.AddHost(AddHostInput{ClientID: id, Host: "a.example.com", Target: "127.0.0.1:81", IsAdmin: true, CertFile: "CERT", KeyFile: "KEY", Scheme: "all"}); err != nil {
		t.Fatal(err)
	}
	return clients, indexes
}

func storeCounts() (clients, tunnels, hosts int) {
	db := file.GetDb().JsonDb
	db.Clients.Range(func(_, _ any) bool { clients++; return true })
	db.Tasks.Range(func(_, _ any) bool { tunnels++; return true })
	db.Hosts.Range(func(_, _ any) bool { hosts++; return true })
	return
}

// A file exported here and renamed apart fits; the ids move and the links
// between the entries follow.
func TestImportAddsUnderNewIDs(t *testing.T) {
	useTempDb(t)
	seedConfig(t)
	transfer := DefaultConfigTransferService{Backend: DefaultBackend()}
	data, err := transfer.Export()
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.NewReplacer("vkey-a", "vkey-b", "alice", "bob", "pw-a", "pw-b", "a.example.com", "b.example.com").Replace(string(data))
	result, err := transfer.Import(ImportConfigInput{Data: []byte(renamed), ReservedAdminUsername: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if result != (ImportResult{Clients: 1, Tunnels: 1, Hosts: 1}) {
		t.Fatalf("result %+v", result)
	}
	if c, tn, h := storeCounts(); c != 2 || tn != 2 || h != 2 {
		t.Fatalf("store has %d clients, %d tunnels, %d hosts", c, tn, h)
	}
	db := file.GetDb()
	id, err := db.GetClientIdByBlake2bVkey(crypt.Blake2b("vkey-b"))
	if err != nil || id == 1 {
		t.Fatalf("imported client id %d: %v", id, err)
	}
	found := db.GetTaskByMd5Password(crypt.Md5("pw-b"))
	if found == nil || found.Client.Id != id {
		t.Fatalf("imported tunnel %+v does not belong to client %d", found, id)
	}
	if host, err := db.FindCertByHost("b.example.com"); err != nil || host.Client.Id != id || host.Id == 1 {
		t.Fatalf("imported host %+v: %v", host, err)
	}
}

// Each conflict leaves the store as it was.
func TestImportRefusalLeavesNothing(t *testing.T) {
	useTempDb(t)
	seedConfig(t)
	transfer := DefaultConfigTransferService{Backend: DefaultBackend()}
	data, err := transfer.Export()
	if err != nil {
		t.Fatal(err)
	}
	// Only the password conflicts, which validation leaves to the store: the
	// client must be taken back after it was added.
	latePassword := strings.NewReplacer("vkey-a", "vkey-b", "alice", "bob", "a.example.com", "b.example.com").Replace(string(data))
	for name, input := range map[string]string{
		"vkey":         string(data),
		"login":        strings.ReplaceAll(string(data), "vkey-a", "vkey-b"),
		"host":         strings.NewReplacer("vkey-a", "vkey-b", "alice", "bob", "pw-a", "pw-b").Replace(string(data)),
		"late":         latePassword,
		"version":      `{"version":99}`,
		"not json":     `<html>`,
		"file mode":    `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5"}],"tunnels":[{"Id":1,"Mode":"file","Client":{"Id":5}}]}`,
		"lost client":  `{"version":1,"tunnels":[{"Id":1,"Mode":"secret","Client":{"Id":5}}]}`,
		"duplicate id": `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5"},{"Id":5,"VerifyKey":"k6"}]}`,
		"same key":     `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5"},{"Id":6,"VerifyKey":"k5"}]}`,
		"bad rule":     `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5","BlackIpList":["not a rule!"]}]}`,
		"limit":        `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5","MaxTunnelNum":1}],"hosts":[{"Host":"x.test","Client":{"Id":5}},{"Host":"y.test","Client":{"Id":5}}]}`,
		"self host":    `{"version":1,"clients":[{"Id":5,"VerifyKey":"k5"}],"hosts":[{"Host":"x.test","Client":{"Id":5}},{"Host":"x.test","Client":{"Id":5}}]}`,
	} {
		if _, err := transfer.Import(ImportConfigInput{Data: []byte(input)}); err == nil {
			t.Errorf("%s: import accepted", name)
		}
		if c, tn, h := storeCounts(); c != 1 || tn != 1 || h != 1 {
			t.Fatalf("%s: store has %d clients, %d tunnels, %d hosts", name, c, tn, h)
		}
	}
	db := file.GetDb()
	if _, err := db.GetClientIdByBlake2bVkey(crypt.Blake2b("vkey-b")); err == nil {
		t.Error("the taken back client still has its vkey index entry")
	}
	if db.GetTaskByMd5Password(crypt.Md5("pw-a")) == nil {
		t.Error("the refused import took the password of the existing tunnel")
	}
}

// A port taken by something else is found before anything is added.
func TestImportRefusesAPortInUse(t *testing.T) {
	useTempDb(t)
	transfer := DefaultConfigTransferService{Backend: DefaultBackend()}
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port
	data, _ := json.Marshal(map[string]any{
		"version": 1,
		"clients": []map[string]any{{"Id": 5, "VerifyKey": "k5"}},
		"tunnels": []map[string]any{{"Id": 1, "Mode": "tcp", "Port": port, "Status": true, "Client": map[string]any{"Id": 5}}},
	})
	if _, err := transfer.Import(ImportConfigInput{Data: data}); err == nil {
		t.Fatal("import accepted")
	}
	if c, tn, _ := storeCounts(); c != 0 || tn != 0 {
		t.Fatalf("store has %d clients, %d tunnels", c, tn)
	}
}

func TestSuggestCertUsesTheCertificateChoice(t *testing.T) {
	useTempDb(t)
	_, indexes := seedConfig(t)
	got := indexes.SuggestCert("a.example.com", 0, 0)
	if got == nil || got.CertFile != "CERT" || got.KeyFile != "KEY" {
		t.Fatalf("suggestion %+v", got)
	}
	if indexes.SuggestCert("a.example.com", got.SourceID, 0) != nil {
		t.Error("the entry being edited was suggested to itself")
	}
	if indexes.SuggestCert("a.example.com", 0, got.SourceID+100) != nil {
		t.Error("the certificate of another client was offered")
	}
	if indexes.SuggestCert("other.example.com", 0, 0) != nil {
		t.Error("an unrelated name got a suggestion")
	}
	// A wildcard certificate serves names below it. An exact entry without
	// certificate files does not hide it.
	client, _ := file.GetDb().GetClient(1)
	if _, err := indexes.AddHost(AddHostInput{ClientID: client.Id, Host: "*.wild.test", Target: "127.0.0.1:81", IsAdmin: true, CertFile: "W", KeyFile: "WK", Scheme: "all"}); err != nil {
		t.Fatal(err)
	}
	if _, err := indexes.AddHost(AddHostInput{ClientID: client.Id, Host: "x.wild.test", Target: "127.0.0.1:81", IsAdmin: true, Scheme: "all"}); err != nil {
		t.Fatal(err)
	}
	if got := indexes.SuggestCert("x.wild.test", 0, 0); got == nil || got.CertFile != "W" {
		t.Fatalf("wildcard suggestion %+v", got)
	}
}

// Through the real service, store, hub and sender to an HTTP receiver.
func TestWebhookReceivesServiceChanges(t *testing.T) {
	useTempDb(t)
	var mu sync.Mutex
	var types []string
	var report []byte
	receiver := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get(webhook.HeaderSignature) != webhook.Sign("topsecret", r.Header.Get(webhook.HeaderTimestamp), body) {
			t.Error("bad signature")
		}
		if strings.Contains(string(body), "vkey-a") || strings.Contains(string(body), "pw-a") {
			t.Errorf("secret in event: %s", body)
		}
		mu.Lock()
		defer mu.Unlock()
		kind := r.Header.Get(webhook.HeaderEventType)
		if kind == server.EventTunnelsReport {
			report = body
			return
		}
		types = append(types, kind)
	}))
	defer receiver.Close()
	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(&server.RunList, 30)
	defer func() { server.Bridge = oldBridge }()
	server.ApplyWebhook(servercfg.WebhookConfig{URL: receiver.URL, Secret: "topsecret", TimeoutSeconds: 2, QueueSize: 64, ReportInterval: 1})
	defer server.ApplyWebhook(servercfg.WebhookConfig{})

	clients, indexes := seedConfig(t)
	tunnel := file.GetDb().GetTaskByMd5Password(crypt.Md5("pw-a"))
	for _, step := range []func() error{
		func() error { return indexes.StopTunnel(tunnel.Id, "") },
		func() error { return indexes.StartTunnel(tunnel.Id, "") },
		func() error { return indexes.ClearTunnel(tunnel.Id, "flow") },
		func() error { return indexes.StopHost(1, "") },
		func() error { return indexes.StartHost(1, "") },
		func() error {
			_, err := indexes.EditHost(EditHostInput{ID: 1, Host: "c.example.com", Target: "127.0.0.1:82", IsAdmin: true, Scheme: "all"})
			return err
		},
		func() error { return clients.Delete(1) }, // takes its tunnel and host along
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"tunnel.created", "host.created",
		"tunnel.stopped", "tunnel.started", "tunnel.updated",
		"host.stopped", "host.started", "host.updated",
		"tunnel.stopped", "tunnel.deleted", "host.deleted",
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), types...)
		mu.Unlock()
		if strings.Join(got, ",") == strings.Join(want, ",") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := report
		mu.Unlock()
		if got != nil {
			var e struct {
				Data server.TunnelsReport `json:"data"`
			}
			if err := json.Unmarshal(got, &e); err != nil || e.Data.Tunnels == nil || e.Data.Hosts == nil {
				t.Fatalf("report %s: %v", got, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no periodic report")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKickRefusesOfflineAndVirtualClients(t *testing.T) {
	r := &virtualClientRuntime{}
	s := DefaultClientService{Backend: Backend{Repository: stubRepository{getClient: func(id int) (*model.Client, error) { return &model.Client{Id: id}, nil }}, Runtime: r}}
	if err := s.Kick(-1); err != ErrVirtualClientOperation {
		t.Fatalf("virtual: %v", err)
	}
	if err := s.Kick(3); err != ErrClientOffline {
		t.Fatalf("offline: %v", err)
	}
	if len(r.disconnected) != 0 {
		t.Fatal("disconnected what is not connected")
	}
}
