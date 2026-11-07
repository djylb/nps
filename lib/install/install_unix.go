//go:build !windows

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// systemBinDirs are the directories that install places the executable in,
// in order of preference.
var systemBinDirs = []string{"/usr/bin", "/usr/local/bin"}

const npsUsage = `nps start|stop|restart|uninstall|update
anywhere!`

// permissionHint tells how to get the rights an update lacked.
const permissionHint = "run the update as root"

// installExecutable copies the executable found in srcDir to the first system
// bin directory that takes it and returns that path. Helper copies named
// <bin>-update left by older installs are removed.
func installExecutable(srcDir, bin string) (string, error) {
	for _, dir := range systemBinDirs {
		_ = os.Remove(filepath.Join(dir, bin+"-update"))
	}
	src := filepath.Join(srcDir, bin)
	var errs []error
	for _, dir := range systemBinDirs {
		dest := filepath.Join(dir, bin)
		target := dest
		if p, err := filepath.EvalSymlinks(dest); err == nil {
			target = p
		}
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = installFile(src, target)
		}
		if err == nil {
			return dest, nil
		}
		errs = append(errs, fmt.Errorf("install %s: %w", dest, err))
	}
	return "", errors.Join(errs...)
}

// installedCopies returns the executables of bin that install placed in the
// system bin directories.
func installedCopies(bin string) []string {
	var paths []string
	for _, dir := range systemBinDirs {
		if p, err := filepath.EvalSymlinks(filepath.Join(dir, bin)); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func executableName(bin string) string { return bin }

// isExecutableName reports whether name is the file name of the executable of
// bin.
func isExecutableName(name, bin string) bool { return name == bin }

func chMod(name string, mode os.FileMode) { _ = os.Chmod(name, mode) }
