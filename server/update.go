package server

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logs"
)

// DisableRemoteUpdate keeps the web interface from updating the server.
var DisableRemoteUpdate bool

// ServiceName is the name nps is installed as a system service under.
const ServiceName = "Nps"

// restartDelay leaves the response to an update request time to reach the
// browser before the server restarts.
const restartDelay = time.Second

var (
	// ErrUpdateDisabled is returned by UpdateSelf when remote update is off.
	ErrUpdateDisabled = errors.New("remote update is disabled")
	// ErrUpdateRunning is returned by UpdateSelf while an update runs.
	ErrUpdateRunning = errors.New("an update is already running")
)

var updating atomic.Bool

// CanUpdateSelf reports whether the web interface may update the server.
func CanUpdateSelf() bool {
	return !DisableRemoteUpdate && install.CanRestartSelf()
}

// UpdateSelf updates the server to the latest release and restarts it
// shortly as the new version. It returns the version updated to, or "" when
// the running version is the latest.
func UpdateSelf() (string, error) {
	if DisableRemoteUpdate {
		return "", ErrUpdateDisabled
	}
	if !updating.CompareAndSwap(false, true) {
		return "", ErrUpdateRunning
	}
	u, err := install.UpdateSelfNps(ServiceName)
	if u == nil {
		updating.Store(false)
		return "", err
	}
	if err != nil {
		logs.Warn("Update to %s: %v", u.Version, err)
	}
	logs.Info("Updated to %s, restarting", u.Version)
	go func() {
		time.Sleep(restartDelay)
		if err := u.Restart(); err != nil {
			logs.Error("Restart after the update to %s failed: %v", u.Version, err)
			updating.Store(false)
		}
	}()
	return u.Version, err
}
