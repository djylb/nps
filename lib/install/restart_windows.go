//go:build windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

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

func relaunch(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), relaunchWaitEnv+"="+strconv.Itoa(os.Getpid()))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	os.Exit(0)
	return nil
}
