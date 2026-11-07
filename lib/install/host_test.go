package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParseCgroupUnit(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0::/system.slice/npc-a.service\n", "npc-a"},
		{"0::/system.slice/Npc.service", "Npc"},
		{"0::/user.slice/user-1000.slice/session-2.scope\n", ""},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/npc.service\n", ""},
		{"0::/\n", ""},
		{"0::/system.slice/docker-1a2b.scope\n", ""},
		// cgroup v1: the name=systemd hierarchy names the unit.
		{"12:memory:/system.slice/npc-b.service\n11:cpu,cpuacct:/system.slice/npc-b.service\n1:name=systemd:/system.slice/npc-b.service\n", "npc-b"},
		{"4:pids:/user.slice\n1:name=systemd:/user.slice/user-0.slice/session-1.scope\n0::/system.slice/x.service\n", ""},
		{"", ""},
	} {
		if got := parseCgroupUnit(c.in); got != c.want {
			t.Errorf("parseCgroupUnit(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLaunchctlList(t *testing.T) {
	out := "PID\tStatus\tLabel\n-\t0\tcom.apple.x\n412\t0\tnpc-a\n4120\t0\tNpc\n"
	for pid, want := range map[int]string{412: "npc-a", 4120: "Npc", 41: "", 0: ""} {
		if got := parseLaunchctlList(out, pid); got != want {
			t.Errorf("parseLaunchctlList(%d) = %q, want %q", pid, got, want)
		}
	}
}

func TestParseScQueryex(t *testing.T) {
	out := "\r\nSERVICE_NAME: Npc\r\nDISPLAY_NAME: nps client\r\n        TYPE               : 10  WIN32_OWN_PROCESS\r\n" +
		"        STATE              : 4  RUNNING\r\n        PID                : 1200\r\n        FLAGS              :\r\n\r\n" +
		"SERVICE_NAME: npc-a\r\nDISPLAY_NAME: npc a\r\n        PID                : 3400\r\n\r\n" +
		"SERVICE_NAME: Stopped\r\n        PID                : 0\r\n"
	for pid, want := range map[int]string{1200: "Npc", 3400: "npc-a", 0: "", 5: ""} {
		if got := parseScQueryex(out, pid); got != want {
			t.Errorf("parseScQueryex(%d) = %q, want %q", pid, got, want)
		}
	}
}

func TestParseUbusServices(t *testing.T) {
	// The npc of the OpenWrt package runs as service "npc"; install's as "Npc".
	out := []byte(`{
	"npc": {"instances": {"instance1": {"running": true, "pid": 2101,
		"command": ["/usr/bin/npc", "-server=a:8024", "-vkey=k", "-type=tcp", "-dns_server=8.8.8.8"]}}},
	"Npc": {"instances": {"instance1": {"running": true, "pid": 2202, "command": ["/usr/bin/npc", "-config=/etc/npc.conf"]}}},
	"dnsmasq": {"instances": {"cfg01411c": {"running": true, "pid": 900, "command": ["/usr/sbin/dnsmasq"]}}},
	"stopped": {"instances": {}}
}`)
	services := parseUbusServices(out)
	for pid, want := range map[int]string{2101: "npc", 2202: "Npc", 900: "dnsmasq", 7: "", 0: ""} {
		if got := procdServiceOf(services, pid); got != want {
			t.Errorf("procdServiceOf(%d) = %q, want %q", pid, got, want)
		}
	}
	if got := procdExecutable(services, "npc"); got != "/usr/bin/npc" {
		t.Errorf("procdExecutable(npc) = %q", got)
	}
	if got := procdExecutable(services, "stopped"); got != "" {
		t.Errorf("procdExecutable(stopped) = %q", got)
	}
	if parseUbusServices([]byte("Command failed")) != nil {
		t.Error("parseUbusServices accepted garbage")
	}
}

func TestParsePlistProgram(t *testing.T) {
	plist := `<plist version="1.0"><dict>
	<key>Label</key>
	<string>npc-a</string>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/npc a/npc-a</string>
		<string>-config=x&amp;y</string>
	</array></dict></plist>`
	if got := parsePlistProgram(plist); got != "/opt/npc a/npc-a" {
		t.Fatalf("parsePlistProgram = %q", got)
	}
	if got := parsePlistProgram(`<key>Program</key><string>/usr/local/bin/npc&amp;</string>`); got != "/usr/local/bin/npc&" {
		t.Fatalf("parsePlistProgram(Program) = %q", got)
	}
	if got := parsePlistProgram(`<key>Label</key><string>x</string>`); got != "" {
		t.Fatalf("parsePlistProgram(none) = %q", got)
	}
}

// fakeHost describes services and their processes to every query of the
// package, whatever the platform the test runs on.
type fakeHost struct {
	platform string
	files    map[string]string
	links    map[string]string
	removed  []string
	// services maps a service to its executable and its process.
	services map[string]fakeService2
}

type fakeService2 struct {
	exe string
	pid int
}

func (f *fakeHost) install(t *testing.T) {
	orig := host
	t.Cleanup(func() { host = orig })
	host = hostSystem{
		readFile: func(name string) ([]byte, error) {
			name = filepath.ToSlash(name)
			if s, ok := f.files[name]; ok {
				return []byte(s), nil
			}
			for svc, s := range f.services {
				switch name {
				case "/proc/" + strconv.Itoa(s.pid) + "/cgroup":
					return []byte("0::/system.slice/" + svc + ".service\n"), nil
				case "/etc/init.d/" + svc:
					return []byte("#!/bin/sh\ncmd=\"" + s.exe + " -config=x\"\n"), nil
				case "/Library/LaunchDaemons/" + svc + ".plist":
					return []byte("<key>ProgramArguments</key><array><string>" + s.exe + "</string></array>"), nil
				}
			}
			return nil, fs.ErrNotExist
		},
		readLink: func(name string) (string, error) {
			if l, ok := f.links[name]; ok {
				return l, nil
			}
			return "", fs.ErrNotExist
		},
		remove: func(name string) error {
			f.removed = append(f.removed, name)
			return nil
		},
		glob: func(pattern string) ([]string, error) {
			var out []string
			for l := range f.links {
				if ok, _ := path.Match(pattern, l); ok {
					out = append(out, l)
				}
			}
			slices.Sort(out)
			return out, nil
		},
		output: func(name string, args ...string) ([]byte, error) {
			var b strings.Builder
			switch name {
			case "ubus":
				b.WriteString("{")
				first := true
				for svc, s := range f.services {
					if !first {
						b.WriteString(",")
					}
					first = false
					fmt.Fprintf(&b, `%q:{"instances":{"instance1":{"pid":%d,"command":[%q,"-vkey=k"]}}}`, svc, s.pid, s.exe)
				}
				b.WriteString("}")
			case "launchctl":
				b.WriteString("PID\tStatus\tLabel\n")
				for svc, s := range f.services {
					fmt.Fprintf(&b, "%d\t0\t%s\n", s.pid, svc)
				}
			case "sc":
				if args[0] == "qc" {
					s, ok := f.services[args[1]]
					if !ok {
						return nil, errors.New("no service")
					}
					fmt.Fprintf(&b, "        BINARY_PATH_NAME   : \"%s\" service\n", s.exe)
					break
				}
				for svc, s := range f.services {
					fmt.Fprintf(&b, "SERVICE_NAME: %s\n        PID                : %d\n", svc, s.pid)
				}
			case "systemctl":
				s, ok := f.services[strings.TrimSuffix(args[len(args)-1], ".service")]
				if !ok {
					return nil, errors.New("no unit")
				}
				fmt.Fprintf(&b, "{ path=%s ; argv[]=%s service ; }\n", s.exe, s.exe)
			default:
				return nil, errors.New("unknown command")
			}
			return []byte(b.String()), nil
		},
		platform: func() string { return f.platform },
	}
}

func TestServiceOfProcess(t *testing.T) {
	if !locatesServices() {
		t.Skip("no service manager queries here")
	}
	f := &fakeHost{platform: "linux-systemd", services: map[string]fakeService2{
		"npc-a": {exe: "/opt/a/npc-a", pid: 300},
		"Npc":   {exe: "/usr/bin/npc", pid: 200},
	}}
	f.install(t)
	if got := serviceOfProcess(300); got != "npc-a" {
		t.Fatalf("serviceOfProcess(300) = %q", got)
	}
	if got := serviceOfProcess(999); got != "" {
		t.Fatalf("serviceOfProcess(999) = %q, want none", got)
	}
	if got := serviceExecutable("npc-a"); got != "/opt/a/npc-a" {
		t.Fatalf("serviceExecutable(npc-a) = %q", got)
	}
}

func TestPidFileService(t *testing.T) {
	f := &fakeHost{files: map[string]string{
		"/var/run/npc-a.pid": "300\n",
		"/var/run/other.pid": "301\n",
		"/etc/init.d/npc-a":  "#!/bin/sh\n",
	}}
	f.install(t)
	host.glob = func(p string) ([]string, error) {
		if p == "/var/run/*.pid" {
			return []string{"/var/run/other.pid", "/var/run/npc-a.pid"}, nil
		}
		return nil, nil
	}
	if got := pidFileService(300); got != "npc-a" {
		t.Fatalf("pidFileService(300) = %q", got)
	}
	// other.pid has no init script: not a service of the init system.
	if got := pidFileService(301); got != "" {
		t.Fatalf("pidFileService(301) = %q", got)
	}
}

// executables creates the named executables in a fresh directory.
func executables(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(paths[i]), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths[i], []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func TestSelectTargets(t *testing.T) {
	p := executables(t, "bin/npc", "usr/bin/npc", "opt/npc-xxx", "srv/npc-xxx", "bin/sh")
	npc, usrNpc, npcXxx, srvXxx, sh := p[0], p[1], p[2], p[3], p[4]
	for _, c := range []struct {
		name        string
		exe, ownExe string
		want        []string
	}{
		// npc run from the command line replaces itself only; /usr/bin/npc
		// and the default service belong to other instances.
		{"command line", npcXxx, "", []string{npcXxx}},
		{"default install", usrNpc, usrNpc, []string{usrNpc}},
		{"renamed service", npcXxx, npcXxx, []string{npcXxx}},
		// The service starts a copy of the running executable.
		{"service runs a copy", npcXxx, srvXxx, []string{npcXxx, srvXxx}},
		{"service runs npc", npcXxx, usrNpc, []string{npcXxx, usrNpc}},
		{"service runs a shell", npc, sh, []string{npc}},
		{"service executable missing", npc, filepath.Join(filepath.Dir(npc), "gone"), []string{npc}},
	} {
		if got := selectTargets("npc", c.exe, c.ownExe); !slices.Equal(got, c.want) {
			t.Errorf("%s: selectTargets = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestServicesToRestart(t *testing.T) {
	if !locatesServices() {
		t.Skip("no service manager queries here")
	}
	p := executables(t, "usr/bin/npc", "opt/npc-a", "opt/npc-b")
	usrNpc, npcA, npcB := p[0], p[1], p[2]
	self := os.Getpid()
	cases := []struct {
		name     string
		services map[string]fakeService2
		targets  []string
		running  []int32
		want     []string
	}{
		{
			// Regression: the default install, updated from the command line.
			name:     "default install",
			services: map[string]fakeService2{"Npc": {exe: usrNpc, pid: 200}},
			targets:  []string{usrNpc},
			running:  []int32{200},
			want:     []string{"Npc"},
		},
		{
			name:     "default service stopped",
			services: map[string]fakeService2{"Npc": {exe: usrNpc, pid: 0}},
			targets:  []string{usrNpc},
			want:     []string{"Npc"},
		},
		{
			// A renamed service is found by its process.
			name:     "renamed service",
			services: map[string]fakeService2{"npc-a": {exe: npcA, pid: 300}},
			targets:  []string{npcA},
			running:  []int32{300},
			want:     []string{"npc-a"},
		},
		{
			// Instances of other executables are not touched.
			name: "several instances",
			services: map[string]fakeService2{
				"Npc":   {exe: usrNpc, pid: 200},
				"npc-a": {exe: npcA, pid: 300},
				"npc-b": {exe: npcB, pid: 400},
			},
			targets: []string{npcA},
			running: []int32{300, 500},
			want:    []string{"npc-a"},
		},
		{
			// The service of the calling process restarts itself.
			name:     "own service",
			services: map[string]fakeService2{"npc-a": {exe: npcA, pid: self}, "npc-b": {exe: npcA, pid: 400}},
			targets:  []string{npcA},
			running:  []int32{400},
			want:     []string{"npc-b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			(&fakeHost{platform: "linux-systemd", services: c.services}).install(t)
			if got := servicesToRestart("Npc", c.targets, c.running); !slices.Equal(got, c.want) {
				t.Fatalf("servicesToRestart = %v, want %v", got, c.want)
			}
		})
	}
}

// TestServicesToRestartProcd checks OpenWrt, where the package's service
// "npc" and install's "Npc" are different services.
func TestServicesToRestartProcd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procd is Linux only")
	}
	p := executables(t, "usr/bin/npc", "opt/npc")
	usrNpc, other := p[0], p[1]
	(&fakeHost{platform: "linux-procd", services: map[string]fakeService2{
		"npc": {exe: usrNpc, pid: 2101},
	}}).install(t)
	if got := servicesToRestart("Npc", []string{usrNpc}, []int32{2101}); !slices.Equal(got, []string{"npc"}) {
		t.Fatalf("servicesToRestart = %v, want [npc]", got)
	}
	// An npc outside procd: neither service is touched.
	if got := servicesToRestart("Npc", []string{other}, nil); len(got) != 0 {
		t.Fatalf("servicesToRestart(other) = %v, want none", got)
	}
	if got := serviceOfProcess(2101); got != "npc" {
		t.Fatalf("serviceOfProcess = %q", got)
	}
	if got := selectTargets("npc", usrNpc, serviceExecutable("npc")); !slices.Equal(got, []string{usrNpc}) {
		t.Fatalf("selectTargets = %v", got)
	}
}

func TestRemoveRcLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("init script links are Unix paths")
	}
	f := &fakeHost{links: map[string]string{
		"/etc/rc.d/S90Npc":  "/etc/init.d/Npc",
		"/etc/rc.d/S50Npc":  "../init.d/Npc",
		"/etc/rc.d/K02Npc":  "/etc/init.d/Npc",
		"/etc/rc2.d/S50Npc": "../init.d/Npc",
		// Links of the order numbers the init script sets.
		"/etc/rc.d/S21Npc": "/etc/init.d/Npc",
		"/etc/rc.d/K89Npc": "/etc/init.d/Npc",
		// Links of other scripts stay.
		"/etc/rc.d/S90Npc-a": "/etc/init.d/Npc-a",
		"/etc/rc3.d/S50Npc":  "/etc/init.d/other",
		// The OpenWrt package's own service, lower case, stays.
		"/etc/rc.d/S95npc": "../init.d/npc",
		"/etc/rc.d/S90npc": "../init.d/npc",
		"/etc/rc.d/K02npc": "/etc/init.d/npc",
	}}
	f.install(t)
	if !removeRcLinks("Npc") {
		t.Fatal("removeRcLinks = false")
	}
	slices.Sort(f.removed)
	want := []string{"/etc/rc.d/K02Npc", "/etc/rc.d/K89Npc", "/etc/rc.d/S21Npc", "/etc/rc.d/S50Npc", "/etc/rc.d/S90Npc", "/etc/rc2.d/S50Npc"}
	if !slices.Equal(f.removed, want) {
		t.Fatalf("removed %v, want %v", f.removed, want)
	}
	f.removed = nil
	if removeRcLinks("Nps") || len(f.removed) != 0 {
		t.Fatalf("removeRcLinks(Nps) removed %v", f.removed)
	}
}

func TestRcOrder(t *testing.T) {
	for script, want := range map[string][2]string{
		"#!/bin/sh /etc/rc.common\nSTART=21\nSTOP=89\n": {"21", "89"},
		"START=95\nUSE_PROCD=1\n":                       {"95", "02"},
		"STOP='5'\n":                                    {"50", "05"},
		"#!/bin/sh\n":                                   {"50", "02"},
	} {
		if start, stop := rcOrder([]byte(script)); start != want[0] || stop != want[1] {
			t.Errorf("rcOrder(%q) = %s, %s, want %s, %s", script, start, stop, want[0], want[1])
		}
	}
}
