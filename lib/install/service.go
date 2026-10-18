package install

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kardianos/service"
)

// ControlService runs a service control action. SysV init systems are driven
// through the init script directly so BusyBox based systems such as OpenWrt
// work as well.
func ControlService(s service.Service, name, action string) error {
	if service.Platform() == "unix-systemv" {
		return exec.Command("/etc/init.d/"+name, action).Run()
	}
	return service.Control(s, action)
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
	if exe := serviceExecutable(name); exe != "" && !containsFile(replaced, exe) {
		fmt.Printf("WARNING: service %s runs %s, which this update did not replace; run install again to point the service at the updated executable\n", name, exe)
		return nil
	}
	if err := ControlService(s, name, "restart"); err != nil {
		return fmt.Errorf("executables replaced, but restarting service %s failed: %w", name, err)
	}
	fmt.Println("Service", name, "restarted")
	return nil
}

func serviceRunning(s service.Service, name string) bool {
	if service.Platform() == "unix-systemv" {
		return exec.Command("/etc/init.d/"+name, "status").Run() == nil
	}
	status, err := s.Status()
	return err == nil && status == service.StatusRunning
}

// serviceExecutable returns the executable the installed service runs, or ""
// when the service manager does not tell.
func serviceExecutable(name string) string {
	switch service.Platform() {
	case "linux-systemd":
		out, err := exec.Command("systemctl", "show", "-p", "ExecStart", "--value", name+".service").Output()
		if err != nil {
			return ""
		}
		return parseSystemdExecStart(string(out))
	case "unix-systemv":
		b, err := os.ReadFile("/etc/init.d/" + name)
		if err != nil {
			return ""
		}
		return parseSysvCmd(string(b))
	case "windows-service":
		out, err := exec.Command("sc", "qc", name).Output()
		if err != nil {
			return ""
		}
		return parseScBinaryPath(string(out))
	}
	return ""
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

// ConfigureService sets the platform specific settings of c.
func ConfigureService(c *service.Config) {
	if service.Platform() != "linux-systemd" {
		return
	}
	c.Dependencies = []string{
		"Requires=network.target",
		"After=network-online.target syslog.target"}
	c.Option["LimitNOFILE"] = 65536
}

// serviceFiles are the files the install of the platform creates, on the
// platforms whose uninstall deletes them only after running the init system.
func serviceFiles(name string) []string {
	switch service.Platform() {
	case "linux-systemd":
		return []string{"/etc/systemd/system/" + name + ".service"}
	case "linux-procd":
		return []string{"/etc/init.d/" + name, "/etc/rc.d/S50" + name, "/etc/rc.d/K02" + name}
	}
	return nil
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
	if service.Platform() == "linux-systemd" {
		_ = exec.Command("systemctl", "daemon-reload").Run()
	}
	return nil
}
