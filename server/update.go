package server

import (
	"context"
	"errors"
	"time"

	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

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

// CanUpdateSelf reports whether the web interface may update the server.
func (s *Server) CanUpdateSelf() bool {
	return !s.noUpdate && install.CanRestartSelf()
}

// UpdateSelf updates the server to the latest release and restarts it
// shortly as the new version. It returns the version updated to, or "" when
// the running version is the latest.
func (s *Server) UpdateSelf() (string, error) {
	if s.noUpdate {
		return "", ErrUpdateDisabled
	}
	if !s.updating.CompareAndSwap(false, true) {
		return "", ErrUpdateRunning
	}
	u, err := install.UpdateSelf("nps", ServiceName)
	if u == nil {
		s.updating.Store(false)
		return "", err
	}
	slogx.Emit(context.Background(), log, &logevent.UpdateApplied, slogx.KeyMsg, "restarting", "to", u.Version)
	go func() {
		time.Sleep(restartDelay)
		if err := u.Restart(); err != nil {
			slogx.Emit(context.Background(), log, &logevent.UpdateFailed, slogx.KeyMsg, "restart after update failed", "to", u.Version, "reason", logevent.NetReason(err), "err", err)
			s.updating.Store(false)
		}
	}()
	return u.Version, nil
}
