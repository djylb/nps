//go:build !windows

package install

import (
	"context"
	"testing"

	"github.com/djylb/selfupdate"
)

// TestUpdateJournalNextToRunning checks that the journal of a pending update
// sits next to the running executable, which confirms or rolls it back, also
// when the update replaces the executable of its service as well.
func TestUpdateJournalNextToRunning(t *testing.T) {
	rs := newReleaseServer(t)
	rs.publish("0.35.9", script("0.35.9"), false)
	p := executables(t, "opt/npc-xxx", "srv/npc-xxx")
	exe, svcExe := p[0], p[1]
	writeExecutable(t, exe, script("0.34.0"))
	writeExecutable(t, svcExe, script("0.34.0"))
	rs.use("0.34.0", exe, svcExe)

	if _, err := Update(context.Background(), "npc", "Npc", UpdateOptions{Pending: true}); err != nil {
		t.Fatalf("Update(pending) error = %v", err)
	}
	assertContent(t, exe, script("0.35.9"))
	assertContent(t, svcExe, script("0.35.9"))
	if out, err := selfupdate.JournalFor(exe).Startup(context.Background(), updateBoots); err != nil || out.Status != selfupdate.StatusPending {
		t.Fatalf("journal next to %s: Startup() = (%v, %v), want pending", exe, out.Status, err)
	}
	if err := selfupdate.JournalFor(exe).Commit(context.Background()); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if out, err := selfupdate.JournalFor(svcExe).Startup(context.Background(), updateBoots); err != nil || out.Status != selfupdate.StatusIdle {
		t.Fatalf("journal next to %s: Startup() = (%v, %v), want idle", svcExe, out.Status, err)
	}
}
