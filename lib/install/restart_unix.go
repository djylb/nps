//go:build !windows

package install

import (
	"github.com/djylb/selfupdate"
	"github.com/kardianos/service"
)

// CanRestartSelf reports whether the calling process can restart itself as a
// new version without its parent noticing. On Unix it always can: the process
// image is replaced in place.
func CanRestartSelf() bool { return true }

// RestartSelf makes the calling process continue as exe, the executable an
// update replaced, and returns only on failure. The process image is replaced
// in place, which keeps its process ID, so a service manager supervising it
// does not see the restart; this holds in containers as well.
func RestartSelf(exe string) error {
	return selfupdate.Relaunch(exe, nil)
}

// restartService restarts the service name, which runs exe, after an update.
// It reports whether the restart was only requested, to happen after the call.
func restartService(s service.Service, name, _ string) (bool, error) {
	return false, ControlService(s, name, "restart")
}
