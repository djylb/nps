package slogx

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	testAuth  = Def{Name: "test_auth_failed", Level: slog.LevelWarn, Security: true, Bus: true}
	testLimit = Def{Name: "test_limited", Level: slog.LevelInfo, Limit: "src_ip"}
	testQuiet = Def{Name: "test_quiet_bus", Level: slog.LevelDebug, Bus: true}
)

func init() { Register(&testAuth, &testLimit, &testQuiet) }

func newBuffered(t *testing.T, level string) *Logs {
	t.Helper()
	l := New()
	if err := l.Configure(Config{Output: "off", Level: level, Memory: 4096}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLineOrderAndQuoting(t *testing.T) {
	l := newBuffered(t, "info")
	Emit(l.Module("auth"), &testAuth, "src_ip", "1.2.3.4", "reason", "bad proof\nlevel=ERROR forged")
	got := l.TakeBuffered()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("not one line: %q", got)
	}
	if !strings.Contains(got, " level=WARN event=test_auth_failed module=auth src_ip=1.2.3.4 reason=\"bad proof\\nlevel=ERROR forged\"\n") {
		t.Fatalf("line = %q", got)
	}
	if !strings.HasPrefix(got, "time=") {
		t.Fatalf("line = %q", got)
	}
}

func TestModuleLevels(t *testing.T) {
	l := newBuffered(t, "warn,net=debug")
	l.Module("auth").Info("hidden")
	l.Module("net").Debug("shown", "k", 1)
	got := l.TakeBuffered()
	if strings.Contains(got, "hidden") || !strings.Contains(got, "module=net k=1 msg=shown") {
		t.Fatalf("got %q", got)
	}
	if err := l.SetLevel("bogus"); err == nil {
		t.Fatal("bad level accepted")
	}
}

func TestSinkGetsFilteredBusEvents(t *testing.T) {
	l := newBuffered(t, "error")
	var got []string
	l.SetSink(func(d *Def, attrs []slog.Attr) { got = append(got, d.Name) })
	Emit(l.Module("x"), &testQuiet, "a", 1)
	l.Module("x").Error("plain")
	if len(got) != 1 || got[0] != "test_quiet_bus" {
		t.Fatalf("sink got %v", got)
	}
	if strings.Contains(l.TakeBuffered(), "test_quiet_bus") {
		t.Fatal("filtered event written")
	}
}

func TestRateLimit(t *testing.T) {
	l := newBuffered(t, "info")
	m := l.Module("x")
	for range 5 {
		Emit(m, &testLimit, "src_ip", "1.1.1.1")
	}
	Emit(m, &testLimit, "src_ip", "2.2.2.2")
	got := l.TakeBuffered()
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("got %q", got)
	}
}

func TestRedactedAndGroups(t *testing.T) {
	l := newBuffered(t, "info")
	l.Module("web").With("req", "r1").WithGroup("g").Info("m", "token", Redacted("s3cret"), "err", errors.New("boom"))
	got := l.TakeBuffered()
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "req=r1 g.token=[REDACTED] g.err=boom msg=m") {
		t.Fatalf("got %q", got)
	}
}

func TestSecurityFileAndJSON(t *testing.T) {
	dir := t.TempDir()
	l := New()
	err := l.Configure(Config{Output: "file", Format: JSON, Level: "error",
		File: Rotation{Path: filepath.Join(dir, "a.log")}, SecurityFile: Rotation{Path: filepath.Join(dir, "sec.log")}})
	if err != nil {
		t.Fatal(err)
	}
	Emit(l.Module("auth"), &testAuth, "src_ip", "9.9.9.9")
	l.Close()
	sec, _ := os.ReadFile(filepath.Join(dir, "sec.log"))
	main, _ := os.ReadFile(filepath.Join(dir, "a.log"))
	if !strings.Contains(string(sec), `"level":"WARN","event":"test_auth_failed","module":"auth","src_ip":"9.9.9.9"}`) {
		t.Fatalf("security log %q", sec)
	}
	if len(main) != 0 {
		t.Fatalf("main log %q", main)
	}
}

func TestFileFallback(t *testing.T) {
	l := New()
	blocker := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(blocker, nil, 0o644)
	if err := l.Configure(Config{Output: "file", File: Rotation{Path: filepath.Join(blocker, "x.log")}, Memory: 4096}); err != nil {
		t.Fatal(err)
	}
	if got := l.TakeBuffered(); !strings.Contains(got, "event=log_fallback") {
		t.Fatalf("got %q", got)
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRotating(Rotation{Path: filepath.Join(dir, "n.log"), MaxSize: 100, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 60) + "\n")
	for range 8 {
		_ = r.Write(line)
	}
	_ = r.Close()
	m, _ := filepath.Glob(filepath.Join(dir, "n-*.log"))
	if len(m) > 2 || len(m) == 0 {
		t.Fatalf("rotated files %v", m)
	}
}

func TestHTTPErrorLog(t *testing.T) {
	l := newBuffered(t, "debug")
	lg := HTTPErrorLog(l.Module("web"), "https")
	lg.Print("http: TLS handshake error from 5.6.7.8:4433: EOF")
	lg.Print("http: something else")
	got := l.TakeBuffered()
	if !strings.Contains(got, "event=tls_handshake_failed module=web src_ip=5.6.7.8 src_port=4433 listener=https reason=eof") ||
		!strings.Contains(got, "level=WARN module=web msg=\"http: something else\"") {
		t.Fatalf("got %q", got)
	}
}
