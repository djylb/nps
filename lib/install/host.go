package install

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kardianos/service"
)

// hostSystem is what this package asks the system about services; tests
// replace it to describe a host without touching the real one.
type hostSystem struct {
	readFile func(name string) ([]byte, error)
	readLink func(name string) (string, error)
	remove   func(name string) error
	glob     func(pattern string) ([]string, error)
	output   func(name string, args ...string) ([]byte, error)
	platform func() string
}

var host = hostSystem{
	readFile: os.ReadFile,
	readLink: os.Readlink,
	remove:   os.Remove,
	glob:     filepath.Glob,
	output:   commandOutput,
	platform: service.Platform,
}

// locatesServices reports whether serviceOfProcess and serviceExecutable
// ask the service manager on this platform.
func locatesServices() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// commandTimeout bounds the service manager queries.
const commandTimeout = 15 * time.Second

func commandOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// parseCgroupUnit returns the systemd system service that the cgroup file
// (/proc/<pid>/cgroup) places the process in, without the ".service" suffix,
// or "" when the process is not in one: a login session, a container, or a
// unit of a user manager, which a system restart would not reach. It reads
// the unified hierarchy of cgroup v2 ("0::/...") and the name=systemd
// hierarchy of cgroup v1.
func parseCgroupUnit(cgroup string) string {
	var path string
	for _, line := range strings.Split(cgroup, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == "name=systemd" {
			path = parts[2]
			break
		}
		if parts[0] == "0" && parts[1] == "" {
			path = parts[2]
		}
	}
	if path == "" || strings.Contains(path, "/user@") {
		return ""
	}
	unit, ok := strings.CutSuffix(path[strings.LastIndex(path, "/")+1:], ".service")
	if !ok || unit == "" {
		return ""
	}
	return unit
}

// parseLaunchctlList returns the label of the job with process pid in the
// output of `launchctl list` ("PID\tStatus\tLabel" rows), or "".
func parseLaunchctlList(out string, pid int) string {
	want := strconv.Itoa(pid)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == want {
			return fields[2]
		}
	}
	return ""
}

// parseScQueryex returns the service whose PID is pid in the output of
// `sc queryex`, or "".
func parseScQueryex(out string, pid int) string {
	var name string
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "SERVICE_NAME":
			name = value
		case "PID":
			if n, err := strconv.Atoi(value); err == nil && n == pid && n != 0 {
				return name
			}
		}
	}
	return ""
}

// parsePlistProgram returns the executable of a launchd plist: the first
// string of ProgramArguments, or Program.
func parsePlistProgram(plist string) string {
	next := func(after string) string {
		_, rest, ok := strings.Cut(plist, after)
		if !ok {
			return ""
		}
		_, rest, ok = strings.Cut(rest, "<string>")
		if !ok {
			return ""
		}
		v, _, _ := strings.Cut(rest, "</string>")
		return unescapeXML(strings.TrimSpace(v))
	}
	if p := next("<key>ProgramArguments</key>"); p != "" {
		return p
	}
	return next("<key>Program</key>")
}

var xmlUnescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#34;", `"`, "&#39;", "'", "&apos;", "'", "&amp;", "&")

func unescapeXML(s string) string { return xmlUnescaper.Replace(s) }

// ubusInstance is an instance in the output of `ubus call service list`.
type ubusInstance struct {
	Pid     int      `json:"pid"`
	Command []string `json:"command"`
}

// parseUbusServices parses the output of `ubus call service list`, the
// services procd runs, by service name.
func parseUbusServices(out []byte) map[string][]ubusInstance {
	var raw map[string]struct {
		Instances map[string]ubusInstance `json:"instances"`
	}
	if json.Unmarshal(out, &raw) != nil {
		return nil
	}
	services := make(map[string][]ubusInstance, len(raw))
	for name, s := range raw {
		for _, in := range s.Instances {
			services[name] = append(services[name], in)
		}
	}
	return services
}

// procdServices asks procd for the services it runs; nil when it does not
// answer.
func procdServices() map[string][]ubusInstance {
	out, err := host.output("ubus", "call", "service", "list")
	if err != nil {
		return nil
	}
	return parseUbusServices(out)
}

// procdServiceOf returns the procd service with an instance of process pid,
// or "".
func procdServiceOf(services map[string][]ubusInstance, pid int) string {
	for name, ins := range services {
		for _, in := range ins {
			if in.Pid == pid && pid != 0 {
				return name
			}
		}
	}
	return ""
}

// procdExecutable returns the executable of the first instance of the procd
// service name, or "".
func procdExecutable(services map[string][]ubusInstance, name string) string {
	for _, in := range services[name] {
		if len(in.Command) > 0 {
			return in.Command[0]
		}
	}
	return ""
}

// pidFileService returns the init script service that wrote pid to its PID
// file, /var/run/<name>.pid or /run/<name>.pid, as the System V and procd
// scripts of the service library do; "" when there is none.
func pidFileService(pid int) string {
	want := strconv.Itoa(pid)
	for _, dir := range []string{"/var/run", "/run"} {
		files, _ := host.glob(dir + "/*.pid")
		for _, f := range files {
			b, err := host.readFile(f)
			if err != nil || strings.TrimSpace(string(b)) != want {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(f), ".pid")
			if _, err := host.readFile("/etc/init.d/" + name); err == nil {
				return name
			}
		}
	}
	return ""
}

// rcLinks are the start and stop links of the init script of name that the
// installs of this and older versions created: S50 and K02 by the service
// library (procd, and System V in /etc/rcN.d), S90 and K02 by older installs
// on OpenWrt.
func rcLinks(name string) []string {
	links := []string{"/etc/rc.d/S50" + name, "/etc/rc.d/S90" + name, "/etc/rc.d/K02" + name}
	for _, n := range []string{"0", "1", "2", "3", "4", "5", "6"} {
		links = append(links, "/etc/rc"+n+".d/S50"+name, "/etc/rc"+n+".d/K02"+name)
	}
	return links
}

// removeRcLinks deletes the start and stop links of the init script of
// name; links to other scripts are left alone. It reports whether one was
// deleted.
func removeRcLinks(name string) bool {
	script := "/etc/init.d/" + name
	removed := false
	for _, l := range rcLinks(name) {
		target, err := host.readLink(l)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(l), target)
		}
		if filepath.Clean(target) != script {
			continue
		}
		if host.remove(l) == nil {
			removed = true
		}
	}
	return removed
}
