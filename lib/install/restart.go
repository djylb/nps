package install

import (
	"context"
	"errors"
	"time"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/selfupdate"
	"github.com/djylb/slogx"
)

var log = slogx.Module("install")

// ErrRestartUnsupported is returned by UpdateSelf and RestartSelf where the
// calling process cannot restart itself reliably (see CanRestartSelf).
var ErrRestartUnsupported = errors.New("a process not run as a Windows service cannot restart itself reliably")

// updateBoots is how many starts of a pending update may end before it
// confirms itself; the next start rolls it back.
const updateBoots = 3

// updateHealthy is how long a pending update has to run before it confirms
// itself.
const updateHealthy = 30 * time.Second

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

// UpdateSelf updates the running bin ("nps" or "npc"), installed as the
// system service name or not, to the latest release as a pending update,
// leaving the restart to the caller; the new version confirms itself in
// StartUpdate. A nil update with a nil error means the running version is
// the latest.
func UpdateSelf(bin, name string) (*SelfUpdate, error) {
	if !CanRestartSelf() {
		return nil, ErrRestartUnsupported
	}
	// Linux reports a replaced executable as deleted, so its path is taken
	// before the update.
	exe, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	res, err := Update(context.Background(), bin, name, UpdateOptions{Pending: true})
	if res == nil {
		return nil, err
	}
	return &SelfUpdate{Version: res.Version, exe: exe}, nil
}

// RecoverUpdate finishes or undoes an update of the running executable that
// a crash interrupted, and deletes what a finished one left behind. Call it
// on every start; StartUpdate does it as well.
func RecoverUpdate() {
	j, err := selfupdate.ExecutableJournal()
	if err == nil {
		_, err = j.Recover(context.Background())
	}
	if err != nil {
		log.Warn("recover the last update failed", "err", err)
	}
}

// StartUpdate is called when the program starts its real work. The first
// starts of a pending update confirm it once they have run for a while; a
// pending update that failed to do so too often is rolled back, and the
// process restarts as the previous version.
func StartUpdate() {
	exe, err := currentExecutable()
	if err != nil {
		log.Warn("check the last update failed", "err", err)
		return
	}
	j := selfupdate.JournalFor(exe)
	out, err := j.Startup(context.Background(), updateBoots)
	if err != nil {
		log.Warn("check the last update failed", "err", err)
		return
	}
	state := selfupdate.Updater{Executable: exe, Journal: j}
	if err := state.RecordOutcome(context.Background(), out); err != nil {
		log.Warn("record the failed update failed", "err", err)
	}
	switch out.Status {
	case selfupdate.StatusPending:
		log.Info("running a pending update; kept if this start stays up", "to", out.Version, "dur_ms", updateHealthy.Milliseconds())
		go func() {
			time.Sleep(updateHealthy)
			if err := j.Commit(context.Background()); err != nil {
				log.Warn("confirm the update failed", "to", out.Version, "err", err)
				return
			}
			log.Info("update confirmed", "to", out.Version)
		}()
	case selfupdate.StatusRolledBack:
		slogx.Emit(context.Background(), log, &logevent.UpdateFailed, slogx.KeyMsg, "rolled back; restarting", "from", out.Previous, "to", out.Version, "reason", "rolled_back")
		if err := RestartSelf(exe); err != nil {
			log.Error("restart after rollback failed, restart manually", "to", out.Previous, "err", err)
		}
	case selfupdate.StatusIdle, selfupdate.StatusInProgress, selfupdate.StatusCleanup:
		// No update to confirm; Startup reports the last two to Peek only.
	}
}
