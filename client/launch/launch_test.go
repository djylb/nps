package launch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	docPayload  = `// a comment
	{ "name": "home", "vkey": "k", /* inline */
	  "paths": [ {"url":"tls://a:1","alpn":"h2","tier":1,"count":2},
	             {"url":"wss://b:443/ws","http":"h2","standby":true}, ],
	  "session": {"max_pipes":2,"scheduler_up":"spread_eta","max_lanes":3,"resume_timeout":9},
	  "access": [ {"access":"p","target":"t:1","local_type":"tcp","local_port":2000,"local_ip":"127.0.0.1","conn_mode":"relay"},
	              {"access":"q","local_type":"mixed","local_port":1080,"auth":[{"user":"u","pass":"1"},{"user":"v","pass":"2"}]} ] }`
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	_, _ = z.Write([]byte(s))
	_ = z.Close()
	return b.Bytes()
}

func TestResolveLink(t *testing.T) {
	got, err := Resolve(context.Background(), "npc://a%3Bb@a:1?type=tls&source_addr=10.0.0.1&session_max_lanes=3&pin=00112233445566778899aabbccddeeff#home")
	if err != nil {
		t.Fatal(err)
	}
	c := got[0].Config.CommonConfig
	if got[0].Name != "home" || !got[0].Direct || c.Server != "tls://a:1" || c.VKey != "a;b" || c.LocalIP != "10.0.0.1" || c.MaxLanes != 3 {
		t.Fatalf("%+v %+v", got[0], c)
	}
}

func TestResolveDocument(t *testing.T) {
	got, err := Resolve(context.Background(), "@"+writeFile(t, "a.jsonc", docPayload))
	if err != nil {
		t.Fatal(err)
	}
	p := got[0]
	c := p.Config.CommonConfig
	if p.Name != "home" || p.Direct || c.VKey != "k" || c.Pipes != "multi" || c.SchedulerUp != "spread_eta" || c.MaxLanes != 3 || c.ResumeTimeout != 9 {
		t.Fatalf("%+v %+v", p, c)
	}
	if !strings.Contains(c.Server, "tls://a:1?") || !strings.Contains(c.Server, "|wss://b:443/ws?") {
		t.Fatal(c.Server)
	}
	l := p.Config.LocalServer
	if l[0].Password != "p" || l[0].Ip != "127.0.0.1" || l[0].ConnMode != "relay" || l[1].Type != "mixed" || l[1].Auth["v"] != "2" {
		t.Fatalf("%+v %+v", l[0], l[1])
	}
	profiles := `{"profiles":{"work":{"vkey":"b","source_addr":"10.0.0.3","paths":[{"url":"tcp://b:1"}]},
		"home":{"vkey":"a","paths":[{"url":"tcp://a:1"}],"access":[{"access":"x","local_port":1}]}}}`
	if got, err = Resolve(context.Background(), "@"+writeFile(t, "l.json", profiles)); err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if home, work := got[0], got[1]; home.Name != "home" || work.Name != "work" || home.Direct || !work.Direct ||
		work.Config.CommonConfig.LocalIP != "10.0.0.3" || !work.written["local_ip"] || len(home.Config.LocalServer) != 1 {
		t.Fatalf("%+v %+v", home, work)
	}
}

// The key of a profile is its name, so "connections" of the first drafts and
// a name inside a profile are refused.
func TestProfilesRejects(t *testing.T) {
	for name, body := range map[string]string{
		"connections list": `{"connections":[{"vkey":"a","paths":[{"url":"tcp://a:1"}]}]}`,
		"name inside":      `{"profiles":{"a":{"name":"b","vkey":"a","paths":[{"url":"tcp://a:1"}]}}}`,
		"empty":            `{"profiles":{}}`,
		"empty name":       `{"profiles":{"":{"vkey":"a","paths":[{"url":"tcp://a:1"}]}}}`,
		"other keys":       `{"profiles":{"a":{"vkey":"a","paths":[{"url":"tcp://a:1"}]}},"vkey":"k"}`,
		"unknown field":    `{"profiles":{"a":{"vkey":"a","source":"1.1.1.1","paths":[{"url":"tcp://a:1"}]}}}`,
		"error names it":   `{"profiles":{"a":{"vkey":"a","paths":[]}}}`,
	} {
		_, err := Resolve(context.Background(), "@"+writeFile(t, "f", body))
		if err == nil || name == "error names it" && !strings.Contains(err.Error(), `"a"`) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Files of installs of earlier versions keep working.
func TestResolveEarlierFiles(t *testing.T) {
	ctx := context.Background()
	for name, body := range map[string]string{
		"ini": iniPayload, "yaml": yamlPayload, "json": `{"server":"a:1","vkey":"k"}`,
		"locals": `{"server":"a:1","vkey":"k","locals":[{"password":"p","local_port":"2000"}]}`,
		"link":   "npc://k@a:1\n", "base64": base64.StdEncoding.EncodeToString([]byte(iniPayload)),
	} {
		if got, err := Resolve(ctx, "@"+writeFile(t, "f", body)); err != nil || len(got) != 1 {
			t.Errorf("%s: %v %+v", name, err, got)
		}
	}
	got, err := Resolve(ctx, "@"+writeFile(t, "f", jsonPayload))
	if err != nil || len(got) != 3 || got[1].Config.CommonConfig.Tp != "tls" || got[2].Config.Tasks[0].Ports != "2222" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestResolveBase64(t *testing.T) {
	ctx := context.Background()
	plain := `{"vkey":"k","paths":[{"url":"tcp://a:1"}]}`
	wrapped := base64.StdEncoding.EncodeToString([]byte(plain))
	wrapped = wrapped[:10] + "\r\n  " + wrapped[10:]
	for name, in := range map[string]string{
		"std": base64.StdEncoding.EncodeToString([]byte(plain)), "url raw": base64.RawURLEncoding.EncodeToString([]byte(plain)),
		"wrapped": wrapped, "gzip": base64.StdEncoding.EncodeToString(gz(t, plain)),
		"link": base64.StdEncoding.EncodeToString([]byte("npc://k@a:1")),
	} {
		if got, err := Resolve(ctx, in); err != nil || got[0].Config.CommonConfig.VKey != "k" {
			t.Errorf("%s: %v", name, err)
		}
	}
	bomb := base64.StdEncoding.EncodeToString(gz(t, strings.Repeat(" ", maxPayloadSize+10)))
	if _, err := Resolve(ctx, bomb); !errors.Is(err, errNotLaunch) {
		t.Errorf("bomb: %v", err)
	}
}

func TestResolveRejects(t *testing.T) {
	ctx := context.Background()
	for name, input := range map[string]string{
		"inline json": `{"server":"a:1","vkey":"k"}`,
		"inline yaml": yamlPayload,
		"inline ini":  iniPayload,
		"npc base64":  "npc://" + base64.RawURLEncoding.EncodeToString([]byte("npc://k@a:1")),
		"plain file":  "config.json",
		"word":        "hello",
		"garbage":     base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe}),
	} {
		_, err := Resolve(ctx, input)
		if err == nil || name != "npc base64" && !errors.Is(err, errNotLaunch) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"unknown key":   `{"vkey":"k","paths":[{"url":"tcp://a:1"}],"x":1}`,
		"no vkey":       `{"paths":[{"url":"tcp://a:1"}]}`,
		"no scheme":     `{"vkey":"k","paths":[{"url":"a:1"}]}`,
		"mixed target":  `{"vkey":"k","paths":[{"url":"tcp://a:1"}],"access":[{"access":"a","local_type":"mixed","target":"t:1"}]}`,
		"auth on tcp":   `{"vkey":"k","paths":[{"url":"tcp://a:1"}],"access":[{"access":"a","local_type":"tcp","auth":[{"user":"u"}]}]}`,
		"profiles+":     `{"profiles":{},"vkey":"k"}`,
		"old link":      "npc://k@a:1?access=pw",
		"old ini local": "[common]\nserver_addr=a:1\nvkey=k\n[p2p_a]\npassword=pw\n",
		"dup name":      `[{"name":"a","server":"a:1","vkey":"k"},{"name":"a","server":"b:1","vkey":"k"}]`,
	} {
		if _, err := Resolve(ctx, "@"+writeFile(t, "f", body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseLocal(t *testing.T) {
	for _, tc := range []struct {
		in        string
		typ, ip   string
		port      int
		target    string
		via, mode string
		accounts  map[string]string
	}{
		{in: "tcp://:2000?access=p1&conn_mode=auto&target=10.0.0.5:53&via=home", typ: "tcp", port: 2000, target: "10.0.0.5:53", via: "home", mode: "auto"},
		{in: "udp://127.0.0.1:53?access=p", typ: "udp", ip: "127.0.0.1", port: 53, mode: "auto"},
		{in: "tcp_udp://[::1]:9?access=p&conn_mode=direct", typ: "tcp_udp", ip: "::1", port: 9, mode: "direct"},
		{in: "transparent://:7?access=p", typ: "transparent", port: 7, mode: "auto"},
		{in: "mixed://0.0.0.0:1080?access=p", typ: "mixed", ip: "0.0.0.0", port: 1080, mode: "auto"},
		{in: "socks5://:1080?access=p&auth=u:1", typ: "socks5", port: 1080, mode: "auto", accounts: map[string]string{"u": "1"}},
		{in: "http://127.0.0.1:8080?access=p", typ: "http", ip: "127.0.0.1", port: 8080, mode: "auto"},
		{in: "mixed://a%40b:p%3Aq@:1080?access=p&auth=u:x:y&auth=v:2", typ: "mixed", port: 1080, mode: "auto",
			accounts: map[string]string{"a@b": "p:q", "u": "x:y", "v": "2"}},
	} {
		l, err := ParseLocal(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		s, err := l.Access.compile()
		if err != nil || s.Type != tc.typ || s.Ip != tc.ip || s.Port != tc.port || s.Target != tc.target || s.ConnMode != tc.mode || l.Via != tc.via {
			t.Fatalf("%s: %+v via=%q %v", tc.in, s, l.Via, err)
		}
		if len(s.Auth) != len(tc.accounts) {
			t.Fatalf("%s: %v", tc.in, s.Auth)
		}
		for u, p := range tc.accounts {
			if s.Auth[u] != p {
				t.Fatalf("%s: %v", tc.in, s.Auth)
			}
		}
	}
	for name, in := range map[string]string{
		"no access": "tcp://:1", "scheme": "ftp://:1?access=p", "no port": "tcp://a?access=p", "unknown": "tcp://:1?access=p&x=1",
		"twice": "tcp://:1?access=p&access=q", "mode": "tcp://:1?access=p&conn_mode=p2p", "port": "tcp://:70000?access=p",
		"mixed target": "mixed://:1?access=p&target=t:1", "transparent target": "transparent://:1?access=p&target=t:1",
		"tcp userinfo": "tcp://u:p@:1?access=p", "tcp auth": "tcp://:1?access=p&auth=u:p", "auth form": "mixed://:1?access=p&auth=u",
		"auth clash":    "mixed://u:1@:1?access=p&auth=u:2",
		"socks5 target": "socks5://:1?access=p&target=t:1", "http target": "http://:1?access=p&target=t:1",
	} {
		if _, err := ParseLocal(in); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "access=p") {
			t.Errorf("%s: the error shows the value: %v", name, err)
		}
	}
}

func sources(values ...string) []Source { return LaunchSources(values) }

func locals(t *testing.T, values ...string) []Local {
	t.Helper()
	l, err := ParseLocals(values)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestComposeVia(t *testing.T) {
	ctx := context.Background()
	plan, err := NewComposer(sources("npc://k@a:1"), locals(t, "tcp://:1?access=p")).Load(ctx)
	if err != nil || len(plan.Profiles) != 1 || plan.Profiles[0].Direct || plan.Profiles[0].Config.LocalServer[0].Password != "p" {
		t.Fatalf("%v %+v", err, plan)
	}
	two := sources("npc://k@a:1#home", "npc://k@b:1#work")
	plan, err = NewComposer(two, locals(t, "tcp://:1?access=p&via=work")).Load(ctx)
	if err != nil || !plan.Profiles[0].Direct || len(plan.Profiles[1].Config.LocalServer) != 1 {
		t.Fatalf("%v %+v", err, plan)
	}
	if _, err = NewComposer(two, locals(t, "tcp://:1?access=p")).Load(ctx); err == nil || !strings.Contains(err.Error(), "via") {
		t.Fatal(err)
	}
	if _, err = NewComposer(two, locals(t, "tcp://:1?access=p&via=nope")).Load(ctx); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatal(err)
	}
	if _, err = NewComposer(nil, locals(t, "tcp://:1?access=p")).Load(ctx); err == nil {
		t.Fatal("no connection accepted")
	}
}

func TestComposeMerge(t *testing.T) {
	ctx := context.Background()
	a := writeFile(t, "a", `{"vkey":"k","paths":[{"url":"tls://a:1"}],"session":{"max_pipes":2},"access":[{"access":"x","local_port":1}]}`)
	b := writeFile(t, "b", `{"name":"n","vkey":"k","paths":[{"url":"tls://a:1"}],"session":{"max_lanes":5},"access":[{"access":"y","local_port":2}]}`)
	plan, err := NewComposer(sources("@"+a, "@"+b, "npc://k@a:1?type=tls"), nil).Load(ctx)
	if err != nil || len(plan.Profiles) != 1 {
		t.Fatalf("%v %+v", err, plan)
	}
	p := plan.Profiles[0]
	if c := p.Config.CommonConfig; c.Pipes != "multi" || c.MaxLanes != 5 || len(p.Config.LocalServer) != 2 || p.Name != "n" {
		t.Fatalf("%+v %+v", p, c)
	}
	// a repeated load must not pile up
	c := NewComposer(sources("@"+a, "@"+b), nil)
	_, _ = c.Load(ctx)
	if plan, _ = c.Load(ctx); len(plan.Profiles[0].Config.LocalServer) != 2 {
		t.Fatalf("%+v", plan.Profiles[0].Config.LocalServer)
	}
	_, err = NewComposer(sources("npc://k@a:1?pipes=multi", "npc://k@a:1?pipes=single"), nil).Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "-launch #1") || !strings.Contains(err.Error(), "-launch #2") {
		t.Fatal(err)
	}
	_, err = NewComposer(sources("npc://k@a:1?tier=1", "npc://k@a:1?tier=2"), nil).Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "paths") {
		t.Fatal(err)
	}
	if _, err = NewComposer(sources("npc://k@a:1#s", "npc://j@b:1#s"), nil).Load(ctx); err == nil {
		t.Fatal("duplicate name accepted")
	}
}

func TestComposePortConflict(t *testing.T) {
	ctx := context.Background()
	link := sources("npc://k@a:1")
	for name, tc := range map[string]struct {
		l    []string
		fail bool
	}{
		"same":      {[]string{"tcp://:1?access=a", "tcp://:1?access=b"}, true},
		"tcp_udp":   {[]string{"tcp_udp://:1?access=a", "udp://:1?access=b"}, true},
		"wildcard":  {[]string{"tcp://127.0.0.1:1?access=a", "mixed://:1?access=b"}, true},
		"networks":  {[]string{"tcp://:1?access=a", "udp://:1?access=b"}, false},
		"addresses": {[]string{"tcp://127.0.0.1:1?access=a", "tcp://127.0.0.2:1?access=b"}, false},
	} {
		_, err := NewComposer(link, locals(t, tc.l...)).Load(ctx)
		if (err != nil) != tc.fail {
			t.Errorf("%s: %v", name, err)
		}
		if tc.fail && (err == nil || !strings.Contains(err.Error(), "-L #1") || !strings.Contains(err.Error(), "-L #2")) {
			t.Errorf("%s: sources not named: %v", name, err)
		}
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
	if got, err := Resolve(ctx, serve(t, 200, `{"name":"y","vkey":"k","paths":[{"url":"tcp://a:1"}]}`)); err != nil || got[0].Name != "y" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := Resolve(ctx, serve(t, 200, "npc://k@a:1#l")); err != nil || got[0].Name != "l" {
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

func TestFetchedContentOnlyDefinesConnections(t *testing.T) {
	t.Setenv("LAUNCH_TEST_SECRET", "s3cret")
	common := "[common]\nserver_addr=a:1\nvkey=k\n"
	conn := `"vkey":"k","paths":[{"url":"tcp://a:1"}]`
	for name, body := range map[string]string{
		"access":        `{` + conn + `,"access":[{"access":"a","local_port":1}]}`,
		"locals":        `{"server":"a:1","vkey":"k","locals":[{"password":"p"}]}`,
		"tasks":         common + "[w]\nmode=tcp\nserver_port=80\ntarget_addr=1.1.1.1:80\n",
		"hosts":         common + "[w]\nhost=a.test\ntarget_addr=1.1.1.1:80\n",
		"template":      common + "remark={{.LAUNCH_TEST_SECRET}}\n",
		"file":          "@/etc/passwd",
		"url":           "http://127.0.0.1:1/x",
		"plain dns":     common + "dns_server=1.1.1.1\n",
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

// Keys only the local user may set stay allowed in a local file.
func TestLocalFileKeepsLocalKeys(t *testing.T) {
	got, err := Resolve(context.Background(), "@"+writeFile(t, "f", "[common]\nserver_addr=a:1\nvkey=k\ndns_server=1.1.1.1\n[web]\nmode=file\nserver_port=80\nlocal_path=/srv\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c := got[0].Config; c.CommonConfig.DnsServer != "1.1.1.1" || c.Tasks[0].LocalPath != "/srv" {
		t.Fatalf("%+v", c.Tasks[0])
	}
}

func TestComposerRemoteFallback(t *testing.T) {
	r, p := SourceRetry, SourcePause
	SourceRetry, SourcePause = 0, 0
	t.Cleanup(func() { SourceRetry, SourcePause = r, p })
	status, body := 200, "npc://k@a:1#remote"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	ctx := context.Background()
	c := NewComposer(sources(srv.URL), locals(t, "tcp://:1?access=p&via=remote"))
	if plan, err := c.Load(ctx); err != nil || len(plan.Profiles) != 1 || len(plan.Profiles[0].Config.LocalServer) != 1 {
		t.Fatalf("%v %+v", err, plan)
	}
	status = 500
	if plan, err := c.Load(ctx); err != nil || len(plan.Profiles) != 1 {
		t.Fatalf("last good: %v %+v", err, plan)
	}
	status = 410
	if plan, err := c.Load(ctx); err != nil || len(plan.Profiles) != 0 {
		t.Fatalf("revoked: %v %+v", err, plan)
	}
}

// A remote source not fetched yet does not stop the local connections, and
// an access that may belong to it waits.
func TestComposerPendingRemote(t *testing.T) {
	c := NewComposer(sources("npc://k@a:1#local", serve(t, 500, "")), locals(t, "tcp://:1?access=p&via=later"))
	plan, err := c.Load(context.Background())
	if err != nil || len(plan.Profiles) != 1 || plan.Retry <= 0 || len(plan.Profiles[0].Config.LocalServer) != 0 {
		t.Fatalf("%v %+v", err, plan)
	}
}

func TestPack(t *testing.T) {
	ctx := context.Background()
	out, err := Pack(ctx, writeFile(t, "a.jsonc", docPayload))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ctx, out)
	if err != nil || got[0].Name != "home" || len(got[0].Config.LocalServer) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err = Pack(ctx, writeFile(t, "b", "@/etc/passwd")); err == nil {
		t.Fatal("packed a reference")
	}
	if _, err = Pack(ctx, writeFile(t, "c", "{nonsense")); err == nil {
		t.Fatal("packed nonsense")
	}
}

func TestShow(t *testing.T) {
	ctx := context.Background()
	src := sources("npc://k%40x@a:1?type=tls&pipes=multi&source_addr=1.1.1.1#home")
	loc := locals(t, "mixed://u:s3@127.0.0.1:1080?access=key", "tcp://:2?access=key&target=t:1&conn_mode=relay")
	masked, err := Show(ctx, src, loc, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"k%40x", "s3", "=key"} {
		if strings.Contains(masked, secret) {
			t.Errorf("masked output shows %s: %s", secret, masked)
		}
	}
	shown, err := Show(ctx, src, loc, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, `-launch="npc://k%40x@a:1?`) || !strings.Contains(shown, "source_addr=1.1.1.1") || !strings.Contains(shown, "auth=u:s3") || !strings.Contains(shown, "access=key") {
		t.Fatal(shown)
	}
	// the printed form is accepted and means the same
	var links, ls []string
	for _, f := range strings.Fields(shown)[1:] {
		v := strings.Trim(f[strings.Index(f, "=")+1:], `"`)
		if strings.HasPrefix(f, "-launch") {
			links = append(links, v)
		} else {
			ls = append(ls, v)
		}
	}
	again, err := Show(ctx, sources(links...), locals(t, ls...), true)
	if err != nil || again != shown {
		t.Fatalf("%v\n%s\n%s", err, shown, again)
	}
}

func TestCommandLine(t *testing.T) {
	ctx := context.Background()
	load := func(f CommandLine, l []Local) ([]Profile, error) {
		src, err := f.Sources()
		if err != nil {
			return nil, err
		}
		plan, err := NewComposer(src, l).Load(ctx)
		return plan.Profiles, err
	}
	p, err := load(CommandLine{Server: "a:1,b:2", VKey: "k1,k2", Type: "tls", Proxy: "socks5://x:1"}, nil)
	if err != nil || len(p) != 2 || !p[0].Direct || p[1].Config.CommonConfig.VKey != "k2" || p[1].Config.CommonConfig.Tp != "tls" {
		t.Fatalf("%v %+v", err, p)
	}
	p, err = load(CommandLine{Server: "a:1", VKey: "k", Type: "tcp", Password: "pw", LocalType: "tcp", ConnMode: "auto", LocalPort: 2000, Target: "t:1"}, nil)
	if l := p[0].Config.LocalServer[0]; err != nil || p[0].Direct || l.Password != "pw" || l.Port != 2000 || l.Target != "t:1" || l.Type != config.LocalTCP {
		t.Fatalf("%v %+v", err, l)
	}
	p, err = load(CommandLine{Config: []string{writeFile(t, "n.conf", iniPayload)}}, nil)
	if err != nil || len(p[0].Config.Tasks) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
	p, err = load(CommandLine{Server: "a:1", VKey: "k", Type: "tcp"}, locals(t, "tcp://:1?access=x"))
	if err != nil || p[0].Direct || len(p[0].Config.LocalServer) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
	src, _ := CommandLine{Server: "a:1", VKey: "k", Type: "tls", Password: "pw", LocalType: "mixed", ConnMode: "relay", LocalPort: 9}.Sources()
	out, err := Show(ctx, src, nil, true)
	if err != nil || !strings.Contains(out, `-launch="npc://k@a:1?type=tls"`) || !strings.Contains(out, `-L="mixed://:9?access=pw&conn_mode=relay"`) {
		t.Fatalf("%v %s", err, out)
	}
}

func TestRoleFromAccess(t *testing.T) {
	ctx := context.Background()
	load := func(links, ls []string) Profile {
		t.Helper()
		plan, err := NewComposer(sources(links...), locals(t, ls...)).Load(ctx)
		if err != nil || len(plan.Profiles) != 1 {
			t.Fatalf("%v %+v", err, plan)
		}
		return plan.Profiles[0]
	}
	if p := load([]string{"npc://k@a:1"}, nil); !p.Direct || p.Hosting {
		t.Fatalf("ordinary: %+v", p)
	}
	if p := load([]string{"npc://k@a:1#v"}, []string{"tcp://:1?access=p&via=v"}); p.Direct || p.Hosting {
		t.Fatalf("visitor: %+v", p)
	}
	mixed := load([]string{"npc://k@a:1", "npc://k@a:1#v"}, []string{"tcp://:1?access=p&via=v"})
	if mixed.Direct || !mixed.Hosting || len(mixed.Config.LocalServer) != 1 {
		t.Fatalf("mixed: %+v", mixed)
	}
	reversed := load([]string{"npc://k@a:1#v", "npc://k@a:1"}, []string{"tcp://:1?access=p&via=v"})
	if !reversed.Hosting || reversed.Name != "v" {
		t.Fatalf("reversed: %+v", reversed)
	}
	// a -L needs via once a connection is written twice
	if _, err := NewComposer(sources("npc://k@a:1", "npc://k@a:1#v"), locals(t, "tcp://:1?access=p")).Load(ctx); err == nil {
		t.Fatal("accepted without via")
	}
	doc := writeFile(t, "d", `{"vkey":"k","paths":[{"url":"tcp://a:1"}],"access":[{"access":"x","local_port":1}]}`)
	if p := load([]string{"@" + doc}, nil); p.Direct || p.Hosting {
		t.Fatalf("file visitor: %+v", p)
	}
	if p := load([]string{"@" + doc, "npc://k@a:1"}, nil); !p.Hosting {
		t.Fatalf("file mixed: %+v", p)
	}
}

// shellWords splits a generated command as the named shell would.
func shellWords(t *testing.T, line, shell string) []string {
	t.Helper()
	var (
		words []string
		cur   strings.Builder
		in    bool // inside a word
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\n':
			if in {
				words, in = append(words, cur.String()), false
				cur.Reset()
			}
		case c == '"':
			in = true
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && shell == "bash" && strings.IndexByte("$`\"\\", line[i+1]) >= 0 {
					i++
				} else if line[i] == '\\' && shell == "cmd" && line[i+1] == '"' {
					i++
				}
				cur.WriteByte(line[i])
			}
		case c == '\'':
			in = true
			for i++; i < len(line); i++ {
				if line[i] != '\'' {
					cur.WriteByte(line[i])
				} else if shell == "powershell" && i+1 < len(line) && line[i+1] == '\'' {
					cur.WriteByte('\'')
					i++
				} else if shell == "bash" && strings.HasPrefix(line[i:], `'\''`) {
					cur.WriteByte('\'')
					i += 3
				} else {
					break
				}
			}
		case c == '\\' && shell == "bash" && i+1 < len(line) && line[i+1] == '\n':
			i++
		case c == '^' && shell == "cmd" && i+1 < len(line) && line[i+1] == '\n':
			i++
		case c == '`' && shell == "powershell" && i+1 < len(line) && line[i+1] == '\n':
			i++
		default:
			in = true
			cur.WriteByte(c)
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}

type generatorSample struct {
	Name, Shell               string
	One, Multi, JSONC, Base64 string
}

func loadGeneratorSamples(t *testing.T) []generatorSample {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "generator_samples.json"))
	if err != nil {
		t.Fatal(err)
	}
	if node, err := exec.LookPath("node"); err == nil {
		fresh, err := exec.Command(node, filepath.Join("testdata", "gen_samples.js")).Output()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fresh, b) {
			t.Fatal("generator_samples.json is stale: node client/launch/testdata/gen_samples.js > client/launch/testdata/generator_samples.json")
		}
	}
	var samples []generatorSample
	if err := json.Unmarshal(b, &samples); err != nil {
		t.Fatal(err)
	}
	return samples
}

// What the web generator prints is what npc accepts, in every output form,
// and the forms agree with each other.
func TestGeneratorSamples(t *testing.T) {
	ctx := context.Background()
	plan := func(t *testing.T, sample generatorSample, words []string) Plan {
		t.Helper()
		var links, ls []string
		for _, w := range words {
			if v, ok := strings.CutPrefix(w, "-launch="); ok {
				links = append(links, v)
			} else if v, ok := strings.CutPrefix(w, "-L="); ok {
				ls = append(ls, v)
			}
		}
		if len(links) != 1 {
			t.Fatalf("%s: %q", sample.Name, words)
		}
		p, err := NewComposer(sources(links...), locals(t, ls...)).Load(ctx)
		if err != nil {
			t.Fatalf("%s: %v", sample.Name, err)
		}
		return p
	}
	for _, s := range loadGeneratorSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			one := plan(t, s, shellWords(t, s.One, s.Shell)).Profiles[0]
			multi := plan(t, s, shellWords(t, s.Multi, s.Shell)).Profiles[0]
			packed := plan(t, s, shellWords(t, s.Base64, s.Shell)).Profiles[0]
			got, err := Resolve(ctx, "@"+writeFile(t, "g.jsonc", s.JSONC))
			if err != nil {
				t.Fatal(err)
			}
			for what, p := range map[string]Profile{"multi": multi, "base64": packed, "jsonc": got[0]} {
				if p.Key() != one.Key() || len(p.Config.LocalServer) != len(one.Config.LocalServer) || p.Config.CommonConfig.Pipes != one.Config.CommonConfig.Pipes {
					t.Fatalf("%s differs: %+v %+v", what, p.Config.CommonConfig, one.Config.CommonConfig)
				}
				for i, l := range p.Config.LocalServer {
					if a := one.Config.LocalServer[i]; l.Password != a.Password || l.Target != a.Target || l.Port != a.Port || l.Ip != a.Ip || len(l.Auth) != len(a.Auth) {
						t.Fatalf("%s local %d: %+v, want %+v", what, i, l, a)
					}
				}
			}
			switch s.Name {
			case "tunnel default":
				if l := one.Config.LocalServer[0]; l.Password != `p&"1!` || one.Config.CommonConfig.VKey != "v k@x$" {
					t.Fatalf("%+v", l)
				}
			case "bash accounts":
				if a := one.Config.LocalServer[0].Auth; a["a b"] != "x:y&z" || a["c"] != "2" {
					t.Fatalf("%v", a)
				}
			case "service named":
				if one.Name != "ho me" || !strings.Contains(s.One, " install ") {
					t.Fatal(s.One)
				}
			case "docker":
				if !strings.HasPrefix(s.One, "docker run -d") {
					t.Fatal(s.One)
				}
			}
		})
	}
}

// A -L reaches a profile of a configuration file by its key, whether or not
// the profile has accesses of its own; only a local port clash is an error.
func TestLocalViaFileProfile(t *testing.T) {
	ctx := context.Background()
	file := writeFile(t, "p.jsonc", `{"profiles":{
		"home":{"vkey":"k","paths":[{"url":"tls://a:1"}]},
		"work":{"vkey":"w","paths":[{"url":"tls://b:1"}],"access":[{"access":"x","local_port":2000,"local_ip":"127.0.0.1"}]}}}`)
	load := func(ls ...string) (Plan, error) {
		return NewComposer(sources("@"+file), locals(t, ls...)).Load(ctx)
	}
	plan, err := load("tcp://:3000?access=p&via=home")
	if err != nil || len(plan.Profiles) != 2 {
		t.Fatalf("%v %+v", err, plan)
	}
	if home := plan.Profiles[0]; home.Name != "home" || home.Direct || home.Hosting || len(home.Config.LocalServer) != 1 {
		t.Fatalf("home: %+v", home)
	}
	if work := plan.Profiles[1]; work.Direct || work.Hosting || len(work.Config.LocalServer) != 1 {
		t.Fatalf("work: %+v", work)
	}
	// a profile with accesses of its own takes more, on other ports
	if plan, err = load("tcp://:3000?access=p&via=work"); err != nil || len(plan.Profiles[1].Config.LocalServer) != 2 {
		t.Fatalf("%v %+v", err, plan)
	}
	// the same connection from a link without access runs as an ordinary client too
	plan, err = NewComposer(sources("@"+file, "npc://k@a:1?type=tls"), locals(t, "tcp://:3000?access=p&via=home")).Load(ctx)
	if err != nil || len(plan.Profiles) != 2 || !plan.Profiles[0].Hosting {
		t.Fatalf("%v %+v", err, plan)
	}
	// only the local port clashes
	for _, l := range []string{"tcp://127.0.0.1:2000?access=p&via=home", "tcp://:2000?access=p&via=work"} {
		if _, err = load(l); err == nil || !strings.Contains(err.Error(), "port 2000") {
			t.Errorf("%s: %v", l, err)
		}
	}
	if _, err = load("tcp://:3000?access=p&via=none"); err == nil || !strings.Contains(err.Error(), "none") {
		t.Fatal(err)
	}
}

// session_usage_interval is a session setting in every form.
func TestUsageIntervalSetting(t *testing.T) {
	ctx := context.Background()
	got, err := Resolve(ctx, "npc://k@a:1?session_usage_interval=30")
	if err != nil || got[0].Config.CommonConfig.UsageInterval != 30 || !got[0].written["session_usage_interval"] {
		t.Fatalf("link: %v %+v", err, got)
	}
	doc := writeFile(t, "u", `{"vkey":"k","paths":[{"url":"tcp://a:1"}],"session":{"usage_interval":-1}}`)
	if got, err = Resolve(ctx, "@"+doc); err != nil || got[0].Config.CommonConfig.UsageInterval != -1 {
		t.Fatalf("file: %v %+v", err, got)
	}
	// written twice and different is the usual session conflict
	_, err = NewComposer(sources("npc://k@a:1?session_usage_interval=30", "npc://k@a:1?session_usage_interval=40"), nil).Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "session_usage_interval") {
		t.Fatal(err)
	}
}
