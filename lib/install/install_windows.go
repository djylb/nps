//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
)

const npsUsage = `nps.exe start|stop|restart|uninstall|update
now!`

// permissionHint tells how to get the rights an update lacked.
const permissionHint = "run the update as administrator"

// installExecutable keeps the running executable where it is and returns its
// path. The helper copy named <bin>-update.exe left by older installs is
// removed.
func installExecutable(_, bin string) (string, error) {
	exe, err := currentExecutable()
	if err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(filepath.Dir(exe), bin+"-update.exe"))
	return exe, nil
}

func executableName(bin string) string { return bin + ".exe" }

// isExecutableName reports whether name is the file name of the executable of
// bin.
func isExecutableName(name, bin string) bool { return strings.EqualFold(name, executableName(bin)) }

// chMod does nothing: Windows has no Unix permission bits.
func chMod(string, os.FileMode) {}
