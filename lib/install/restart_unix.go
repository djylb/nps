//go:build !windows

package install

import (
	"fmt"
	"os"
	"syscall"
)

// restartDetached is only needed on Windows, which cannot replace a process
// image in place.
func restartDetached(exe string) error {
	return fmt.Errorf("detached restart of %s is only used on Windows", exe)
}

func relaunch(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
