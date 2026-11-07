package install

import (
	"errors"
	"fmt"
	"os"
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

// LinkServiceStart creates the start and stop links of the System V init
// script of name in /etc/rc.d, which BusyBox based systems such as OpenWrt
// read and the service library does not create. The names match those of
// procd, so that a later install on either removes them.
func LinkServiceStart(name string) {
	if host.platform() != "unix-systemv" {
		return
	}
	if fi, err := os.Stat("/etc/rc.d"); err != nil || !fi.IsDir() {
		return
	}
	script := "/etc/init.d/" + name
	_ = os.Symlink(script, "/etc/rc.d/S50"+name)
	_ = os.Symlink(script, "/etc/rc.d/K02"+name)
}

// serviceRunning reports whether the service s is running.
func serviceRunning(s service.Service, name string) bool {
	if running, known := serviceStatus(name); known {
		return running
	}
	status, err := s.Status()
	return err == nil && status == service.StatusRunning
}

// UninstallService uninstalls s, the system service name. systemd refuses to
// disable a unit whose file is empty, and procd runs the init script, which
// an empty or foreign script fails; the files of the service are then deleted
// directly so that a later install does not find them in place. The start
// and stop links of the init script, which installs of this and older
// versions named differently, are removed as well.
func UninstallService(s service.Service, name string) error {
	if usesRcLinks() {
		defer removeRcLinks(name)
	}
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
// init script.
func parseSysvCmd(script string) string {
	for _, line := range strings.Split(script, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), `cmd="`); ok {
			if fields := strings.Fields(strings.TrimSuffix(rest, `"`)); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// parseScBinaryPath extracts the executable from the BINARY_PATH_NAME line of
// `sc qc` output.
func parseScBinaryPath(out string) string {
	for _, line := range strings.Split(out, "\n") {
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
