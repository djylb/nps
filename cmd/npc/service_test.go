//go:build !sdk

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/nps/client/launch"
	flag "github.com/spf13/pflag"
)

func serviceFlags(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("npc", flag.ContinueOnError)
	fs.SortFlags = false
	fs.StringP("server", "s", "", "")
	fs.StringP("vkey", "k", "", "")
	fs.StringP("type", "t", "tcp", "")
	fs.StringP("config", "c", "", "")
	fs.String("proxy", "", "")
	fs.String("local_ip", "", "")
	fs.String("pipes", "single", "")
	fs.String("local_type", "tcp_udp", "")
	fs.String("conn_mode", "auto", "")
	fs.Int("local_port", 2000, "")
	fs.String("password", "", "")
	fs.String("target", "", "")
	fs.String("log", "file", "")
	fs.String("pprof", "", "")
	fs.Int("disconnect_timeout", 30, "")
	fs.Bool("debug", true, "")
	fs.StringArray("launch", nil, "")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs
}

func installArgs(t *testing.T, launchEnv string, args ...string) (string, []string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "conf")
	svc, files, err := serviceArguments(serviceFlags(t, args...), dir, launchEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeServiceFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	return dir, svc
}

func resolveFile(t *testing.T, path string) []launch.Profile {
	t.Helper()
	p, err := launch.Resolve(context.Background(), "@"+path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServiceArgumentsKeepConnectionOut(t *testing.T) {
	dir, svc := installArgs(t, "", "--server=a.example:8024,b.example:8025", "-k", "k1,k2",
		"--type=tcp,tls", "--proxy=socks5://u:p@127.0.0.1:1080", "--local_ip=10.0.0.1",
		"--log=off", "--pprof=127.0.0.1:9999", "--disconnect_timeout=40", "install")
	conn := filepath.Join(dir, serviceConnFile)
	want := []string{"--launch=@" + conn, "--log=off", "--pprof=127.0.0.1:9999", "--disconnect_timeout=40", "-debug=false"}
	if !slices.Equal(svc, want) {
		t.Fatalf("service args = %q, want %q", svc, want)
	}
	for _, a := range svc {
		if strings.Contains(a, "k1") || strings.Contains(a, "example") || strings.Contains(a, "socks5") {
			t.Fatalf("service args carry connection settings: %q", svc)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(conn)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	p := resolveFile(t, conn)
	if len(p) != 2 {
		t.Fatalf("profiles = %d, want 2", len(p))
	}
	for i, w := range [][3]string{{"a.example:8024", "k1", "tcp"}, {"b.example:8025", "k2", "tls"}} {
		c := p[i].Config.CommonConfig
		if !p[i].Direct || c.Server != w[0] || c.VKey != w[1] || c.Tp != w[2] ||
			c.ProxyUrl != "socks5://u:p@127.0.0.1:1080" || c.LocalIP != "10.0.0.1" {
			t.Fatalf("profile %d = direct %v %+v, want %v", i, p[i].Direct, c, w)
		}
	}
}

func TestServiceArgumentsPassword(t *testing.T) {
	dir, svc := installArgs(t, "", "-s", "a:1", "-k", "k", "--password=pw", "--local_type=tcp", "--conn_mode=relay",
		"--local_port=3000", "--target=10.0.0.2:22")
	conn := filepath.Join(dir, serviceConnFile)
	if !slices.Equal(svc, []string{"--launch=@" + conn, "-debug=false"}) {
		t.Fatalf("service args = %q", svc)
	}
	p := resolveFile(t, conn)
	if len(p) != 1 || len(p[0].Config.LocalServer) != 1 {
		t.Fatalf("profiles = %+v", p)
	}
	l := p[0].Config.LocalServer[0]
	if l.Type != "tcp" || l.ConnMode != "relay" || l.Password != "pw" || l.Port != 3000 || l.Target != "10.0.0.2:22" || p[0].Config.CommonConfig.VKey != "k" {
		t.Fatalf("local server = %+v", l)
	}
}

func TestServiceArgumentsConfigBesideCommand(t *testing.T) {
	dir, svc := installArgs(t, "", "-s", "a:1", "-k", "k", "-c", "conf/a.conf, b.conf")
	a, _ := filepath.Abs("conf/a.conf")
	b, _ := filepath.Abs("b.conf")
	want := []string{"--launch=@" + filepath.Join(dir, serviceConnFile), "--launch=@" + a, "--launch=@" + b, "-debug=false"}
	if !slices.Equal(svc, want) {
		t.Fatalf("service args = %q, want %q", svc, want)
	}
}

func TestServiceArgumentsConfigOnly(t *testing.T) {
	dir, svc := installArgs(t, "", "-c", "conf/npc.conf", "--log=stdout")
	abs, _ := filepath.Abs("conf/npc.conf")
	if !slices.Equal(svc, []string{"--config=" + abs, "--log=stdout", "-debug=false"}) {
		t.Fatalf("service args = %q", svc)
	}
	if _, err := os.Stat(filepath.Join(dir, serviceConnFile)); !os.IsNotExist(err) {
		t.Fatalf("connection file written for a config only install: %v", err)
	}
}

func TestServiceArgumentsLaunch(t *testing.T) {
	payload := `{"server":"a:1","vkey":"k"}`
	dir, svc := installArgs(t, "", "--launch="+payload, "--launch=@local.yaml", "-s", "x:1", "-k", "y")
	file := filepath.Join(dir, "npc-service-launch-1.conf")
	abs, _ := filepath.Abs("local.yaml")
	if !slices.Equal(svc, []string{"--launch=@" + file, "--launch=@" + abs, "-debug=false"}) {
		t.Fatalf("service args = %q", svc)
	}
	if p := resolveFile(t, file); len(p) != 1 || p[0].Config.CommonConfig.VKey != "k" {
		t.Fatalf("profiles = %+v", p)
	}

	dir, svc = installArgs(t, payload)
	if !slices.Equal(svc, []string{"--launch=@" + filepath.Join(dir, "npc-service-launch-1.conf"), "-debug=false"}) {
		t.Fatalf("NPC_LAUNCH service args = %q", svc)
	}
}

func TestServiceArgumentsHalfCommand(t *testing.T) {
	if _, _, err := serviceArguments(serviceFlags(t, "-s", "a:1"), t.TempDir(), ""); err == nil {
		t.Fatal("install with -server and no -vkey succeeded")
	}
}

func TestWriteServiceFilesKeepsUserFiles(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "npc.conf")
	if err := os.WriteFile(user, []byte("[common]\nvkey=user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "npc-service-launch-2.conf")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, files, err := serviceArguments(serviceFlags(t, "-s", "a:1", "-k", "k"), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeServiceFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(user); string(b) != "[common]\nvkey=user\n" {
		t.Fatalf("npc.conf changed: %q", b)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale launch file kept: %v", err)
	}
}

func TestServiceKeepsCandidates(t *testing.T) {
	server := "tls://a.com:8025|wss://a.com:443/ws?tier=1&sni=b.com|tcp://[::1]:8024?standby=1,c.com:8024@d.com/p"
	dir, _ := installArgs(t, "", "--server="+server, "--vkey=k1,k2", "--type=tcp,wss", "--pipes=multi")
	b, err := os.ReadFile(filepath.Join(dir, serviceConnFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"server": "tls://a.com:8025|wss://a.com:443/ws?tier=1&sni=b.com|tcp://[::1]:8024?standby=1,c.com:8024@d.com/p"`) ||
		!strings.Contains(string(b), `"pipes": "multi"`) {
		t.Fatalf("service file:\n%s", b)
	}
	profiles := resolveFile(t, filepath.Join(dir, serviceConnFile))
	if len(profiles) != 2 {
		t.Fatalf("%d profiles", len(profiles))
	}
	c0, c1 := profiles[0].Config.CommonConfig, profiles[1].Config.CommonConfig
	if c0.Server != "tls://a.com:8025|wss://a.com:443/ws?tier=1&sni=b.com|tcp://[::1]:8024?standby=1" || c0.Pipes != "multi" ||
		c1.Server != "c.com:8024@d.com/p" || c1.Tp != "wss" {
		t.Fatalf("profiles %+v %+v", c0, c1)
	}
	if _, _, err := serviceArguments(serviceFlags(t, "--server=ftp://a.com", "--vkey=k"), t.TempDir(), ""); err == nil {
		t.Fatal("bad candidate accepted")
	}
}
