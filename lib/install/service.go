package install

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kardianos/service"
)

// ControlService runs a service control action, the way the service manager
// of the platform needs it (see controlService).
func ControlService(s service.Service, name, action string) error {
	if handled, err := controlService(s, name, action); handled {
		return err
	}
	return service.Control(s, action)
}

// restartWithTimeout restarts s, giving up when it does not stop within
// timeout.
func restartWithTimeout(s service.Service, name string, timeout time.Duration) error {
	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop() }()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-stopped:
		if err != nil {
			return err
		}
	case <-t.C:
		return fmt.Errorf("service %s did not stop within %v; restart it manually", name, timeout)
	}
	return s.Start()
}

// RestartAfterUpdate restarts the services that ran the executables res
// replaced (see UpdateResult.Services), so they run the new version. s is the
// service name, the default service of the program, or nil when the service
// manager is unavailable; the other services are reached by their names. A
// service that is not running takes the new version on its next start.
func RestartAfterUpdate(s service.Service, name string, res *UpdateResult) error {
	if len(res.Services) == 0 {
		fmt.Println("No service runs the updated executable; the new version takes effect on the next start")
		return nil
	}
	var errs []error
	for _, svc := range res.Services {
		target := s
		if svc != name || s == nil {
			var err error
			if target, err = namedService(svc); err != nil {
				errs = append(errs, fmt.Errorf("executables replaced, but restarting service %s failed: %w", svc, err))
				continue
			}
		}
		if !serviceRunning(target, svc) {
			fmt.Println("Service", svc, "is not running; the new version takes effect on its next start")
			continue
		}
		exe := serviceExecutable(svc)
		if exe == "" {
			exe = res.Replaced[0]
		}
		requested, err := restartService(target, svc, exe)
		if err != nil {
			errs = append(errs, fmt.Errorf("executables replaced, but restarting service %s failed: %w", svc, err))
			continue
		}
		if requested {
			fmt.Println("Service", svc, "restart requested")
		} else {
			fmt.Println("Service", svc, "restarted")
		}
	}
	return errors.Join(errs...)
}

// nopProgram is the program of a service that is only controlled, not run.
type nopProgram struct{}

func (nopProgram) Start(service.Service) error { return nil }
func (nopProgram) Stop(service.Service) error  { return nil }

// namedService returns a handle to control the installed service name.
func namedService(name string) (service.Service, error) {
	return service.New(nopProgram{}, &service.Config{Name: name})
}

// LinkServiceStart sets the start and stop links of the init script of name
// in /etc/rc.d, which BusyBox based systems such as OpenWrt read: the
// service library creates none for System V and fixed S50 and K02 ones for
// procd. The links it leaves are replaced by ones with the START and STOP of
// the script, so that the script's own enable and enabled see the same
// links and a later enable adds none.
func LinkServiceStart(name string) {
	if p := host.platform(); p != "unix-systemv" && p != "linux-procd" {
		return
	}
	if fi, err := os.Stat("/etc/rc.d"); err != nil || !fi.IsDir() {
		return
	}
	script := "/etc/init.d/" + name
	b, err := host.readFile(script)
	if err != nil {
		return
	}
	start, stop := rcOrder(b)
	removeRcLinks(name)
	_ = os.Symlink(script, "/etc/rc.d/S"+start+name)
	_ = os.Symlink(script, "/etc/rc.d/K"+stop+name)
}

// rcOrder returns the START and STOP order numbers an init script sets, as
// two digits; 50 and 02 when it sets none.
func rcOrder(script []byte) (start, stop string) {
	start, stop = "50", "02"
	for line := range strings.SplitSeq(string(script), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.Trim(v, "\"' "))
		if err != nil || n < 0 || n > 99 {
			continue
		}
		switch k {
		case "START":
			start = fmt.Sprintf("%02d", n)
		case "STOP":
			stop = fmt.Sprintf("%02d", n)
		}
	}
	return start, stop
}

// serviceRunning reports whether the service s is running.
func serviceRunning(s service.Service, name string) bool {
	if running, known := serviceStatus(name); known {
		return running
	}
	status, err := s.Status()
	return err == nil && status == service.StatusRunning
}

// UninstallService stops and uninstalls s, the system service name. systemd refuses to
// disable a unit whose file is empty, and procd runs the init script, which
// an empty or foreign script fails; the files of the service are then deleted
// directly so that a later install does not find them in place. The start
// and stop links of the init script, which installs of this and older
// versions named differently, are removed as well.
func UninstallService(s service.Service, name string) error {
	if usesRcLinks() {
		defer removeRcLinks(name)
	}
	// A running service is stopped first: the service managers remove its
	// definition but leave the process running, holding its ports, with
	// nothing left to stop it. Not running is no error.
	_ = service.Control(s, "stop")
	err := service.Control(s, "uninstall")
	if err == nil {
		return nil
	}
	removed := false
	for _, p := range serviceFiles(name) {
		if os.Remove(p) == nil {
			removed = true
		}
	}
	if !removed {
		return err
	}
	serviceFilesRemoved()
	return nil
}

// parseSystemdExecStart extracts the path from `systemctl show -p ExecStart
// --value` output such as "{ path=/usr/bin/nps ; argv[]=/usr/bin/nps service ; ... }".
func parseSystemdExecStart(out string) string {
	_, rest, ok := strings.Cut(out, "path=")
	if !ok {
		return ""
	}
	if i := strings.IndexAny(rest, " ;\n"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// parseSysvCmd extracts the executable from the cmd= line of the System V
// or procd init script: cmd='<path>' as SetServiceScripts writes it, or
// cmd="<path> <arguments>" as the service library and earlier versions did.
func parseSysvCmd(script string) string {
	for line := range strings.SplitSeq(script, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "cmd='"); ok {
			return shUnquote("'" + rest)
		}
		if rest, ok := strings.CutPrefix(line, `cmd="`); ok {
			if fields := strings.Fields(strings.TrimSuffix(rest, `"`)); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// shUnquote reads one word of single-quoted parts, as shQuote writes it.
func shUnquote(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		switch s[0] {
		case '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return b.String() + s[1:]
			}
			b.WriteString(s[1 : 1+end])
			s = s[2+end:]
		case '\\':
			if len(s) > 1 {
				b.WriteByte(s[1])
				s = s[2:]
			} else {
				s = s[1:]
			}
		default:
			return b.String()
		}
	}
	return b.String()
}

// parseScBinaryPath extracts the executable from the BINARY_PATH_NAME line of
// `sc qc` output.
func parseScBinaryPath(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "BINARY_PATH_NAME" {
			continue
		}
		value = strings.TrimSpace(value)
		if rest, ok := strings.CutPrefix(value, `"`); ok {
			path, _, _ := strings.Cut(rest, `"`)
			return path
		}
		if i := strings.Index(strings.ToLower(value), ".exe"); i >= 0 {
			return value[:i+len(".exe")]
		}
		if fields := strings.Fields(value); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}
