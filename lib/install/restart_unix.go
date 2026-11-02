//go:build !windows

package install

import "fmt"

// restartDetached is only needed on Windows, which cannot replace a process
// image in place.
func restartDetached(exe string) error {
	return fmt.Errorf("detached restart of %s is only used on Windows", exe)
}
