package install

import (
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

// RestartAfterUpdate restarts the service so it runs the replaced executables.
// Only an installed and running service is restarted; otherwise the new
// version takes effect on the next start. When the service is known to run an
// executable outside replaced it is left alone, since a restart would not
// pick up the update. A nil s means the service manager is unavailable.
func RestartAfterUpdate(s service.Service, name string, replaced []string) error {
	if s == nil || !serviceRunning(s, name) {
		fmt.Println("Service", name, "is not running; the new version takes effect on its next start")
		return nil
	}
	exe := serviceExecutable(name)
	if exe != "" && !containsFile(replaced, exe) {
		fmt.Printf("WARNING: service %s runs %s, which this update did not replace; run install again to point the service at the updated executable\n", name, exe)
		return nil
	}
	if exe == "" {
		exe = replaced[0]
	}
	requested, err := restartService(s, name, exe)
	if err != nil {
		return fmt.Errorf("executables replaced, but restarting service %s failed: %w", name, err)
	}
	if requested {
		fmt.Println("Service", name, "restart requested")
	} else {
		fmt.Println("Service", name, "restarted")
	}
	return nil
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
// directly so that a later install does not find them in place.
func UninstallService(s service.Service, name string) error {
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
