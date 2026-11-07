//go:build windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/kardianos/service"
)

// CanRestartSelf reports whether the calling process can restart itself as a
// new version without its parent noticing. A Windows process has to be started
// anew; that is only reliable for a service, which the service manager
// restarts, while whatever started any other process would see it exit.
func CanRestartSelf() bool { return !service.Interactive() }

// RestartSelf makes the service process continue as exe, the executable an
// update replaced: it hands the restart to the service manager and returns at
// once, to be stopped by it shortly. Other processes cannot restart
// themselves.
func RestartSelf(exe string) error {
	if !CanRestartSelf() {
		return ErrRestartUnsupported
	}
	return restartDetached(exe)
}

// restartService restarts the service name, which runs exe, after an update.
// It reports whether the restart was only requested, to happen after the call:
// the service process itself cannot wait for a restart that ends it.
func restartService(s service.Service, name, exe string) (bool, error) {
	if !service.Interactive() {
		return true, restartDetached(exe)
	}
	return false, ControlService(s, name, "restart")
}

const (
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
)

// startDetached starts exe with args outside the console, process group and,
// where the job allows it, the job object of the caller, so that it survives
// the service manager ending the service process that started it.
func startDetached(exe string, args ...string) (*os.Process, error) {
	flags := uint32(detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP)
	p, err := startWithFlags(exe, args, flags|createBreakawayFromJob)
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		// the job of the caller forbids breaking away
		p, err = startWithFlags(exe, args, flags)
	}
	return p, err
}

func startWithFlags(exe string, args []string, flags uint32) (*os.Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// restartDetached restarts the service that runs exe without waiting for the
// restart. The service process itself may call it: stopping the service ends
// the caller, so exe is run detached to stop and start the service again.
func restartDetached(exe string) error {
	p, err := startDetached(exe, "restart")
	if err != nil {
		return err
	}
	return p.Release()
}
