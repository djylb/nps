package launch

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	iniPayload  = "[common]\nserver_addr=a:1\nvkey=k\nconn_type=tls\n[ssh]\nmode=tcp\nserver_port=22\ntarget_addr=127.0.0.1:22\n"
	jsonPayload = `{"profiles":[
		{"name":"d","server":"a:1,b:2","vkey":"k1,k2","type":"TLS"},
		{"name":"c","common":{"server_addr":"c:3","vkey":"k3","flow_limit":1000000},
		 "hosts":[{"remark":"web","host":"x.test","target_addr":["1.1.1.1:80","2.2.2.2:80"],"headers":{"X-A":"b"}}],
		 "tasks":[{"mode":"tcp","server_port":2222,"target_addr":"1.1.1.1:22"}]}]}`
	yamlPayload = "name: y\nserver: a:1\nvkey: k\n"
)

func TestResolveForms(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(file, []byte(yamlPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, input string
		names       []string
		direct      bool
		check       func(*testing.T, []Profile)
	}{
		{name: "npc", input: "npc://k@a:1?type=TLS&local_ip=10.0.0.1", names: []string{"profile-1"}, direct: true,
			check: func(t *testing.T, p []Profile) {
				c := p[0].Config.CommonConfig
				if c.Server != "a:1" || c.VKey != "k" || c.Tp != "tls" || c.LocalIP != "10.0.0.1" {
					t.Fatalf("%+v", c)
				}
			}},
		{name: "npc list", input: "npc://?server=a:1,b:2&vkey=k&type=tls,tcp", names: []string{"profile-1#1", "profile-1#2"}, direct: true,
			check: func(t *testing.T, p []Profile) {
				c := p[1].Config.CommonConfig
				if c.Server != "b:2" || c.VKey != "k" || c.Tp != "tcp" {
					t.Fatalf("%+v", c)
				}
			}},
		{name: "npc local", input: "npc://k@a:1?password=pw&target=t:22&local_port=2000", names: []string{"profile-1"},
			check: func(t *testing.T, p []Profile) {
				l := p[0].Config.LocalServer[0]
				if l.Password != "pw" || l.Target != "t:22" || l.Port != 2000 || l.TargetType != "all" || !l.Fallback {
					t.Fatalf("%+v", l)
				}
			}},
		{name: "ini", input: iniPayload, names: []string{"profile-1"},
			check: func(t *testing.T, p []Profile) {
				if len(p[0].Config.Tasks) != 1 || p[0].Config.Tasks[0].Remark != "ssh" {
					t.Fatalf("%+v", p[0].Config.Tasks)
				}
			}},
		{name: "yaml", input: yamlPayload, names: []string{"y"}, direct: true},
		{name: "file", input: "@" + file, names: []string{"y"}, direct: true},
		{name: "base64", input: base64.StdEncoding.EncodeToString([]byte(iniPayload)), names: []string{"profile-1"}},
		{name: "base64 url unpadded", input: base64.RawURLEncoding.EncodeToString([]byte(yamlPayload)), names: []string{"y"}, direct: true},
		{name: "yaml sections", input: "common: {server_addr: a:1, vkey: k}\nlocals:\n  - {password: pw, local_type: secret, local_port: 9}\n", names: []string{"profile-1"},
			check: func(t *testing.T, p []Profile) {
				if p[0].Config.LocalServer[0].Type != "secret" {
					t.Fatalf("%+v", p[0].Config.LocalServer[0])
				}
			}},
		{name: "list", input: `[{"server":"a:1","vkey":"k"},{"server":"b:1","vkey":"k"}]`, names: []string{"profile-1", "profile-2"}, direct: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.names) {
				t.Fatalf("got %d profiles", len(got))
			}
			for i, p := range got {
				if p.Name != tt.names[i] || p.Direct != tt.direct {
					t.Fatalf("profile %d: %q direct=%v", i, p.Name, p.Direct)
				}
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestResolveJSON(t *testing.T) {
	got, err := Resolve(context.Background(), jsonPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "d#1" || got[1].Config.CommonConfig.Server != "b:2" || got[1].Config.CommonConfig.Tp != "tls" {
		t.Fatalf("%+v", got)
	}
	c := got[2].Config
	if got[2].Direct || c.CommonConfig.Client.Flow.FlowLimit != 1000000 {
		t.Fatalf("%+v", c.CommonConfig.Client.Flow)
	}
	h := c.Hosts[0]
	if h.Remark != "web" || h.Target.TargetStr != "1.1.1.1:80\n2.2.2.2:80" || h.HeaderChange != "X-A:b\n" {
		t.Fatalf("%+v", h)
	}
	if c.Tasks[0].Remark != "task_1" || c.Tasks[0].Ports != "2222" {
		t.Fatalf("%+v", c.Tasks[0])
	}
}

func TestResolveRejects(t *testing.T) {
	for name, input := range map[string]string{
		"empty":            " ",
		"no vkey":          "npc://a:1",
		"unknown param":    "npc://k@a:1?skip_verify=true",
		"unknown key":      `{"server":"a:1","vkey":"k","x":1}`,
		"duplicate name":   `[{"name":"a","server":"a:1","vkey":"k"},{"name":"a","server":"b:1","vkey":"k"}]`,
		"line break":       `{"server":"a:1","vkey":"k\n[x]\nmode=file"}`,
		"reserved remark":  `{"common":{"server_addr":"a:1","vkey":"k"},"tasks":[{"remark":"p2p1","password":"p"}]}`,
		"local without pw": "npc://k@a:1?target=t:22",
		"plain word":       "hello",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Resolve(context.Background(), input); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func serve(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetch(t *testing.T) {
	ctx := context.Background()
	if got, err := Resolve(ctx, serve(t, 200, yamlPayload)); err != nil || got[0].Name != "y" {
		t.Fatalf("%v %v", got, err)
	}
	var remote *RemoteError
	if _, err := Resolve(ctx, serve(t, 500, "")); !errors.As(err, &remote) || remote.Revoked {
		t.Fatalf("500: %v", err)
	}
	if _, err := Resolve(ctx, serve(t, 410, "")); !errors.As(err, &remote) || !remote.Revoked {
		t.Fatalf("410: %v", err)
	}
	if _, err := Resolve(ctx, serve(t, 200, strings.Repeat("a", maxPayloadSize+1))); !errors.As(err, &remote) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestFetchedPayloadIsUntrusted(t *testing.T) {
	t.Setenv("LAUNCH_TEST_SECRET", "s3cret")
	common := `[common]` + "\nserver_addr=a:1\nvkey=k\n"
	for name, body := range map[string]string{
		"template":      common + "remark={{.LAUNCH_TEST_SECRET}}\n",
		"file":          "@/etc/passwd",
		"url":           "http://127.0.0.1:1/x",
		"cert":          common + "[w]\nhost=a\ncert_file=/etc/passwd\n",
		"multi account": common + "[w]\nmode=tcp\nmulti_account=/etc/passwd\n",
		"pprof":         `{"common":{"server_addr":"a:1","vkey":"k","pprof_addr":"0.0.0.0:6060"},"tasks":[]}`,
		"file tunnel":   `{"common":{"server_addr":"a:1","vkey":"k"},"tasks":[{"mode":"file","local_path":"/"}]}`,
		"base64 nested": base64.StdEncoding.EncodeToString([]byte("@/etc/passwd")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(context.Background(), serve(t, 200, body))
			var remote *RemoteError
			if !errors.As(err, &remote) {
				t.Fatalf("expected a remote error, got %v", err)
			}
		})
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	profiles, err := Resolve(context.Background(), "npc://k@127.0.0.1:1?password=pw&target=127.0.0.1:2&local_port=0&target_type=tcp")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	uuids := make(map[string]string)
	go func() { done <- Run(ctx, profiles, uuids, Options{P2PTimeout: 1}) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("returned before cancel: %v", err)
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}
