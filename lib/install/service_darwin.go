//go:build darwin

package install

import (
	"os"
	"path/filepath"

	"github.com/kardianos/service"
)

// controlService leaves every action to the service library.
func controlService(_ service.Service, _, _ string) (bool, error) { return false, nil }

// serviceStatus leaves the status to the service library.
func serviceStatus(_ string) (running, known bool) { return false, false }

// launchdPlists are where the service library places the plist of the
// launchd job name: a system daemon, or an agent of the user.
func launchdPlists(name string) []string {
	paths := []string{"/Library/LaunchDaemons/" + name + ".plist"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "Library", "LaunchAgents", name+".plist"))
	}
	return paths
}

// serviceExecutable returns the executable the launchd job name runs, read
// from its plist, or "".
func serviceExecutable(name string) string {
	for _, p := range launchdPlists(name) {
		if b, err := host.readFile(p); err == nil {
			return parsePlistProgram(string(b))
		}
	}
	return ""
}

// serviceOfProcess returns the label of the launchd job that runs the
// process pid, or "" when launchd does not list it.
func serviceOfProcess(pid int) string {
	out, err := host.output("launchctl", "list")
	if err != nil {
		return ""
	}
	return parseLaunchctlList(string(out), pid)
}

// ConfigureService sets the platform specific settings of c; there are none.
func ConfigureService(_ *service.Config) {}

// serviceFiles returns nothing: uninstall leaves no files behind here.
func serviceFiles(_ string) []string { return nil }

func serviceFilesRemoved() {}

func usesRcLinks() bool { return false }
