package obstest

import (
	"log/slog"
	"testing"

	"github.com/djylb/nps/lib/obs"
)

var ev = obs.Def{Name: "obstest_ev", Level: slog.LevelWarn}

func TestRecorder(t *testing.T) {
	l, r := New()
	obs.Emit(l.Module("m"), &ev, "src_ip", "1.2.3.4")
	l.Module("m").Debug("d", "k", 1)
	if e := r.Events("obstest_ev"); len(e) != 1 || e[0].Attrs["src_ip"] != "1.2.3.4" || e[0].Module != "m" {
		t.Fatalf("events %+v", e)
	}
	if len(r.Lines()) != 2 {
		t.Fatalf("lines %+v", r.Lines())
	}
}
