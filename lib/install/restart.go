package install

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/kardianos/service"
	"github.com/shirou/gopsutil/v4/process"
)

// relaunchWaitEnv names the variable through which Relaunch passes its
// process ID to the process that replaces it on Windows.
const relaunchWaitEnv = "NPS_RELAUNCH_WAIT"

// relaunchWait bounds how long a relaunched process waits for its
// predecessor to exit.
const relaunchWait = 30 * time.Second

// ErrRestartUnsupported is returned by UpdateSelf where the calling process
// cannot restart itself reliably.
var ErrRestartUnsupported = errors.New("a process not run as a Windows service cannot restart itself reliably")

// CanRestartSelf reports whether the calling process can restart itself as a
// new version without its parent noticing. On Unix the process image is
// replaced in place. A Windows process has to be started anew; that is only
// reliable for a service, which the service manager restarts, while whatever
// started any other process would see it exit.
func CanRestartSelf() bool {
	return runtime.GOOS != "windows" || !service.Interactive()
}

// SelfUpdate is an update of the running executable whose restart is
// still to come.
type SelfUpdate struct {
	// Version is the version updated to.
	Version string
	exe     string
}

// Restart makes the calling process continue as the new version; see
// RestartSelf.
func (u *SelfUpdate) Restart() error {
	return RestartSelf(u.exe)
}

// UpdateSelfNpc updates the running npc, installed as the system service
// name or not, to the latest release and restarts it as the new version. It
// returns nil without restarting when the running version is the latest;
// otherwise it returns only on failure, or at once for a Windows service,
// which the service manager then restarts.
func UpdateSelfNpc(name string) error {
	u, err := updateSelf("client", "npc", name)
	if u == nil {
		return err
	}
	return u.Restart()
}

// UpdateSelfNps updates the running nps, installed as the system service
// name or not, to the latest release, leaving the restart to the caller. A
// nil update with a nil error means the running version is the latest; an
// update with an error means the executables were replaced but the web
// assets were not, and the restart is still due.
func UpdateSelfNps(name string) (*SelfUpdate, error) {
	return updateSelf("server", "nps", name)
}

func updateSelf(kind, bin, name string) (*SelfUpdate, error) {
	if !CanRestartSelf() {
		return nil, ErrRestartUnsupported
	}
	// Linux reports a replaced executable as deleted, so its path is taken
	// before the update.
	exe, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	// An error with a result means the executables were replaced, so the
	// restart is still due.
	res, err := update(kind, bin, name, false)
	if res == nil {
		return nil, err
	}
	return &SelfUpdate{Version: res.Version, exe: exe}, err
}

// RestartSelf makes the calling process continue as exe, the executable an
// update replaced, and returns only on failure, with one exception: a Windows
// service hands the restart to the service manager and returns at once, to
// be stopped by it shortly. On Unix the process image is replaced in place,
// which keeps its process ID, so a service manager supervising it does not
// see the restart; this holds in containers as well.
func RestartSelf(exe string) error {
	if runtime.GOOS == "windows" && !service.Interactive() {
		return restartDetached(exe)
	}
	return Relaunch(exe)
}

// Relaunch replaces the calling process, which does not run as a service,
// with exe run with the same arguments and environment. On Unix the process
// image is replaced in place. Windows has no such call: exe is started to
// wait for the caller to exit, and the caller exits.
func Relaunch(exe string) error {
	return relaunch(exe)
}

// WaitForRelaunch waits, in a process started by Relaunch, until the process
// it replaces has exited and released what it held, such as its ports.
func WaitForRelaunch() {
	v := os.Getenv(relaunchWaitEnv)
	if v == "" {
		return
	}
	_ = os.Unsetenv(relaunchWaitEnv)
	pid, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return
	}
	waitForExit(int32(pid), relaunchWait)
}

func waitForExit(pid int32, timeout time.Duration) {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		if ok, err := process.PidExists(pid); err != nil || !ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
