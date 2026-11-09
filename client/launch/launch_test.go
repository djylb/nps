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

	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/lib/config"
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
		{name: "npc", input: "npc://k@a:1?type=tls&local_ip=10.0.0.1&session_max_lanes=3&pin=00112233445566778899aabbccddeeff#home", names: []string{"home"}, direct: true,
			check: func(t *testing.T, p []Profile) {
				c := p[0].Config.CommonConfig
				if c.Server != "tls://a:1" || c.VKey != "k" || c.LocalIP != "10.0.0.1" || c.MaxLanes != 3 || c.Pin != "00112233445566778899aabbccddeeff" {
					t.Fatalf("%+v", c)
				}
			}},
		{name: "npc vkey with list separators", input: "npc://a%3Bb%2Cc@a:1?type=tls", names: []string{"profile-1"}, direct: true,
			check: func(t *testing.T, p []Profile) {
				if c := p[0].Config.CommonConfig; c.VKey != "a;b,c" || c.Server != "tls://a:1" {
					t.Fatalf("%+v", c)
				}
			}},
		{name: "npc local", input: "npc://k@a:1?access=pw&target=t:22&local_port=2000&name=v", names: []string{"v"},
			check: func(t *testing.T, p []Profile) {
				l := p[0].Config.LocalServer[0]
				if l.Password != "pw" || l.Target != "t:22" || l.Port != 2000 || l.Type != config.LocalTCPUDP || l.ConnMode != config.ConnAuto {
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
		{name: "yaml sections", input: "common: {server_addr: a:1, vkey: k}\nlocals:\n  - {password: pw, local_type: udp, conn_mode: relay, local_port: 9}\n", names: []string{"profile-1"},
			check: func(t *testing.T, p []Profile) {
				if p[0].Config.LocalServer[0].Type != config.LocalUDP || p[0].Config.LocalServer[0].ConnMode != config.ConnRelay {
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
		"newer format":     "npc://7:k@a:1",
		"link type":        "npc://k@a:1?type=kcp",
		"link pin":         "npc://k@a:1?pin=zz",
		"link twice":       "npc://k@a:1?access=a&password=b",
		"unknown key":      `{"server":"a:1","vkey":"k","x":1}`,
		"duplicate name":   `[{"name":"a","server":"a:1","vkey":"k"},{"name":"a","server":"b:1","vkey":"k"}]`,
		"line break":       `{"server":"a:1","vkey":"k\n[x]\nmode=file"}`,
		"reserved remark":  `{"common":{"server_addr":"a:1","vkey":"k"},"tasks":[{"remark":"p2p1","password":"p"}]}`,
		"local without pw": "npc://k@a:1?target=t:22",
		"old link type":    "npc://k@a:1?access=pw&local_type=p2ps",
		"old local type":   `{"common":{"server_addr":"a:1","vkey":"k"},"locals":[{"password":"pw","local_type":"p2ps"}]}`,
		"old ini local":    "[common]\nserver_addr=a:1\nvkey=k\n[p2p_a]\npassword=pw\n",
		"bad conn mode":    `{"common":{"server_addr":"a:1","vkey":"k"},"locals":[{"password":"pw","conn_mode":"p2p"}]}`,
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
			if _, ok := errors.AsType[*RemoteError](err); !ok {
				t.Fatalf("expected a remote error, got %v", err)
			}
		})
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	connect.Install()
	profiles, err := Resolve(context.Background(), "npc://k@127.0.0.1:1?access=pw&target=127.0.0.1:2&local_port=0&local_type=tcp")
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

// A bare carriage return ends a line for the configuration parser, so it must
// not hide a key from the checks on fetched payloads.
func TestFetchedPayloadCannotUseLineEndings(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "accounts")
	if err := os.WriteFile(secret, []byte("user=pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	head := "[common]\nserver_addr=a:1\nvkey=k\n"
	web := head + "[web]\nmode=tcp\nserver_port=80\ntarget_addr=1.1.1.1:80\n"
	for name, body := range map[string]string{
		"multi_account": web + "x=y\rmulti_account=" + secret + "\n",
		"local_path":    web + "x=y\rlocal_path=/etc\n",
		"mode file":     head + "[web]\nserver_port=80\rmode=file\n",
		"dns_server":    head + "x=y\rdns_server=1.1.1.1\n",
		"plain dns":     head + "dns_server=1.1.1.1\n",
		"pprof":         head + "x=y\rpprof_addr=127.0.0.1:0\n",
		"host cert":     head + "[h]\nhost=x.test\rcert_file=" + secret + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolve(context.Background(), body, false, 0)
			if err == nil || !strings.Contains(err.Error(), "may not") {
				t.Fatalf("got %v, %+v", err, got)
			}
		})
	}
}

// Keys only the local user may set stay allowed in a local payload.
func TestLocalPayloadKeepsLocalKeys(t *testing.T) {
	got, err := Resolve(context.Background(), "[common]\nserver_addr=a:1\nvkey=k\ndns_server=1.1.1.1\n[web]\nmode=file\nserver_port=80\nlocal_path=/srv\n")
	if err != nil {
		t.Fatal(err)
	}
	if c := got[0].Config; c.CommonConfig.DnsServer != "1.1.1.1" || c.Tasks[0].LocalPath != "/srv" {
		t.Fatalf("%+v", c.Tasks[0])
	}
}

// Profiles that stop at once write their uuids while the next ones start.
func TestRunUuidsWithManyProfiles(t *testing.T) {
	connect.Install()
	var profiles []Profile
	for i := range 8 {
		got, err := Resolve(context.Background(), "npc://k@127.0.0.1:1?name=p"+string(rune('0'+i)))
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, got...)
	}
	uuids := map[string]string{}
	if err := Run(context.Background(), profiles, uuids, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(uuids) != len(profiles) {
		t.Fatalf("%v", uuids)
	}
}

func TestResolveBase64(t *testing.T) {
	b64 := base64.RawURLEncoding.EncodeToString
	link := "npc://k@a:1?type=tls"
	wrapped := base64.StdEncoding.EncodeToString([]byte(iniPayload))
	var lines []string
	for len(wrapped) > 20 {
		lines, wrapped = append(lines, wrapped[:20]), wrapped[20:]
	}
	lines = append(lines, wrapped)

	// plain base64 tolerates line breaks and padding, and may hold many connections
	for name, in := range map[string]string{
		"wrapped":  strings.Join(lines, "\n"),
		"crlf":     strings.Join(lines, "\r\n  "),
		"unpadded": strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(iniPayload)), "="),
	} {
		if got, err := Resolve(context.Background(), in); err != nil || len(got) != 1 || len(got[0].Config.Tasks) != 1 {
			t.Errorf("plain %s: %v %+v", name, err, got)
		}
	}
	if got, err := Resolve(context.Background(), base64.StdEncoding.EncodeToString([]byte(jsonPayload))); err != nil || len(got) != 3 {
		t.Errorf("plain, many connections: %v %d", err, len(got))
	}

	// npc://<base64> is one connection in any form
	for name, in := range map[string]string{
		"link": link, "ini": iniPayload, "yaml": yamlPayload,
		"json":   `{"server":"a:1","vkey":"k"}`,
		"nested": "npc://" + b64([]byte(link)),
	} {
		got, err := Resolve(context.Background(), "npc://"+b64([]byte(in)))
		if err != nil || len(got) != 1 {
			t.Errorf("%s: %v %+v", name, err, got)
		}
	}
	// one instance may have several paths
	paths := "npc://k@a:1?type=tls&pipes=multi&count=2&alt=tcp://b:2?tier=1;weight=2"
	if got, err := Resolve(context.Background(), "npc://"+b64([]byte(paths))); err != nil || len(got) != 1 ||
		got[0].Config.CommonConfig.Server != "tls://a:1?count=2|tcp://b:2?tier=1&weight=2" || got[0].Config.CommonConfig.Pipes != "multi" {
		t.Errorf("several paths: %v %+v", err, got)
	}
	got, _ := Resolve(context.Background(), "npc://"+b64([]byte(link)))
	if c := got[0].Config.CommonConfig; c.Server != "tls://a:1" || c.VKey != "k" || !got[0].Direct {
		t.Errorf("%+v", c)
	}

	for name, in := range map[string]string{
		"many":        "npc://" + b64([]byte(jsonPayload)),
		"many urls":   "npc://" + b64([]byte(`{"server":"a:1,b:1","vkey":"k"}`)),
		"white space": "npc://" + b64([]byte(link))[:8] + "\n" + b64([]byte(link))[8:],
		"not base64":  "npc://%%%",
		"word":        "npc://hello",
		"garbage":     "npc://" + b64([]byte{0xff, 0xfe, 0x00}),
	} {
		_, err := Resolve(context.Background(), in)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if name != "many" && name != "many urls" && !strings.Contains(err.Error(), "neither vkey@host nor valid base64") && name != "garbage" {
			t.Errorf("%s: %v", name, err)
		}
	}

	// the decoded content keeps the trust of the payload that held it
	body := "npc://" + b64([]byte("[common]\nserver_addr=a:1\nvkey=k\ndns_server=1.1.1.1\n"))
	if _, err := resolve(context.Background(), body, false, 0); err == nil || !strings.Contains(err.Error(), "may not") {
		t.Errorf("a fetched payload set dns_server: %v", err)
	}
	if _, err := resolve(context.Background(), body, true, 0); err != nil {
		t.Errorf("a local payload could not: %v", err)
	}
}
