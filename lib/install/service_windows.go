//go:build windows

package install

import (
	"os/exec"
	"time"

	"github.com/kardianos/service"
)

// windowsStopTimeout bounds the wait for the Windows service to stop on a
// restart. The service library waits for the stop without a bound.
const windowsStopTimeout = 60 * time.Second

// controlService restarts the service with a bounded wait for the stop; it
// leaves the other actions to the service library.
func controlService(s service.Service, name, action string) (bool, error) {
	if action == "restart" {
		return true, restartWithTimeout(s, name, windowsStopTimeout)
	}
	return false, nil
}

// serviceStatus leaves the status to the service library.
func serviceStatus(string) (running, known bool) { return false, false }

// serviceExecutable returns the executable the installed service runs, or ""
// when the service manager does not tell.
func serviceExecutable(name string) string {
	out, err := exec.Command("sc", "qc", name).Output()
	if err != nil {
		return ""
	}
	return parseScBinaryPath(string(out))
}

// ConfigureService sets the platform specific settings of c; Windows has none.
func ConfigureService(*service.Config) {}

// serviceFiles returns nothing: the service manager keeps no files.
func serviceFiles(string) []string { return nil }

func serviceFilesRemoved() {}
