package slogx

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestConfigureWhileLogging(t *testing.T) {
	dir := t.TempDir()
	l := New()
	defer l.Close()
	cfg := Config{Output: "file", File: Rotation{Path: filepath.Join(dir, "a.log")}, Async: 64}
	if err := l.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			m := l.Module("x")
			for {
				select {
				case <-stop:
					return
				default:
					m.Info("line", "k", 1)
				}
			}
		})
	}
	for range 20 {
		if err := l.Configure(cfg); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestRotatingWriteAfterClose(t *testing.T) {
	r, err := OpenRotating(Rotation{Path: filepath.Join(t.TempDir(), "a.log")})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if !errors.Is(r.Write([]byte("x\n")), os.ErrClosed) {
		t.Fatal("write after close did not fail")
	}
	if r.f != nil {
		t.Fatal("write after close reopened the file")
	}
}

func TestPruneLeavesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	keep := []string{"app-access.log", "app-old.log", "app-20261108T000000.000.txt", "app-.log", "other.log"}
	for _, n := range keep {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	r, err := OpenRotating(Rotation{Path: filepath.Join(dir, "app.log"), MaxSize: 10, MaxFiles: 1, MaxAge: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		_ = r.Write([]byte("0123456789\n"))
	}
	_ = r.Close()
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("pruned %s", n)
		}
	}
}

func TestRotationNamesAreUnique(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRotating(Rotation{Path: filepath.Join(dir, "a.log"), MaxSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		_ = r.Write([]byte{byte('a' + i), 'b', 'c', '\n'})
	}
	_ = r.Close()
	files := r.rotatedFiles()
	if len(files) != 19 {
		t.Fatalf("%d rotated files, want 19 (none overwritten)", len(files))
	}
}

func TestRotateEvery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		r, err := OpenRotating(Rotation{Path: filepath.Join(dir, "a.log"), MaxSize: -1, Every: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Write([]byte("one\n"))
		time.Sleep(time.Hour)
		_ = r.Write([]byte("two\n"))
		_ = r.Close()
		if n := len(r.rotatedFiles()); n != 1 {
			t.Fatalf("%d rotated files", n)
		}
	})
}

func TestCompressAndPruneInOrder(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRotating(Rotation{Path: filepath.Join(dir, "a.log"), MaxSize: 8, MaxFiles: 3, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 30 {
		_ = r.Write([]byte("01234567\n"))
	}
	_ = r.Close()
	files := r.rotatedFiles()
	if len(files) != 3 {
		t.Fatalf("%d rotated files kept", len(files))
	}
	for _, f := range files {
		if !strings.HasSuffix(f.path, ".gz") {
			t.Fatalf("uncompressed %s", f.path)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(m) > 0 {
		t.Fatalf("temporary files left: %v", m)
	}
}

func TestRaiseOrderIsStable(t *testing.T) {
	for range 200 {
		l := newLevels()
		_ = l.Raise("", slog.LevelDebug, time.Hour)
		_ = l.Raise("db", slog.LevelWarn, time.Hour)
		if got := l.Of("db"); got != slog.LevelDebug {
			t.Fatalf("db at %v, want debug: a default raise applies to every module", got)
		}
		l.stop()
	}
}

func TestRaiseBoundsAndClose(t *testing.T) {
	l := newLevels()
	l.MaxRaises = 2
	_ = l.Raise("a", slog.LevelDebug, time.Hour)
	_ = l.Raise("b", slog.LevelDebug, time.Hour)
	if err := l.Raise("c", slog.LevelDebug, time.Hour); !errors.Is(err, ErrTooManyRaises) {
		t.Fatalf("third raise: %v", err)
	}
	if err := l.Raise("a", slog.LevelDebug, 0); err != nil || l.Of("a") != slog.LevelInfo {
		t.Fatal("zero ttl did not end the raise")
	}
	l.stop()
	if err := l.Raise("a", slog.LevelDebug, time.Hour); err == nil {
		t.Fatal("raise after close accepted")
	}
	if l.Of("b") != slog.LevelInfo {
		t.Fatal("raise survived close")
	}
}

func TestSamplerOverflowIsLimited(t *testing.T) {
	var sm sampler
	now := time.Now()
	s := Sampling{First: 1, Tick: time.Minute}
	passed := 0
	for i := range MaxSampleKeys + 1000 {
		if _, ok := sm.allow("ip"+itoa(i), s, now); ok {
			passed++
		}
	}
	if passed != MaxSampleKeys+1 || len(sm.seen) != MaxSampleKeys+1 {
		t.Fatalf("passed %d with %d keys: new keys beyond the table must share one limit", passed, len(sm.seen))
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

type nilErr struct{ p *int }

func (e *nilErr) Error() string { return "v" + itoa(*e.p) }

func TestPanickingValueDoesNotCrash(t *testing.T) {
	l, o := withRoute(t, Config{}, nil)
	var e *nilErr
	l.Module("x").Info("m", "err", error(e))
	if !strings.Contains(o.all(), "!PANIC") {
		t.Fatalf("got %q", o.all())
	}
	l2, o2 := withRoute(t, Config{}, JSONEncoder{})
	l2.Module("x").Info("m", "err", error(e))
	if !strings.Contains(o2.all(), "!PANIC") {
		t.Fatalf("got %q", o2.all())
	}
}

func TestAttributesCannotForgeFixedFields(t *testing.T) {
	l, o := withRoute(t, Config{}, JSONEncoder{})
	l.Module("x").Info("real", "level", "DEBUG", "msg", "forged", "time", "0", "module", "admin", "event", "login_ok")
	got := o.all()
	for _, k := range []string{`"level":"DEBUG"`, `"msg":"forged"`, `"module":"admin"`, `"event":"login_ok"`} {
		if strings.Contains(got, k) {
			t.Fatalf("forged %s in %s", k, got)
		}
	}
	if !strings.Contains(got, `"_level":"DEBUG"`) || !strings.Contains(got, `"module":"x"`) {
		t.Fatalf("got %s", got)
	}
}

func TestMemorySurvivesConfigure(t *testing.T) {
	l := New()
	defer l.Close()
	_ = l.Configure(Config{Output: "off", Memory: 10})
	m := l.Memory()
	sub := m.Subscribe(context.Background(), Query{}, 4)
	l.Module("x").Info("one")
	if l.TakeBuffered() == "" {
		t.Fatal("nothing taken")
	}
	_ = l.Configure(Config{Output: "off", Memory: 10})
	if l.Memory() != m {
		t.Fatal("memory replaced")
	}
	l.Module("x").Info("two")
	if s := l.TakeBuffered(); !strings.Contains(s, "msg=two") || strings.Contains(s, "msg=one") {
		t.Fatalf("take after configure %q", s)
	}
	_ = l.Configure(Config{Output: "off", Memory: 20})
	n := 0
	for range sub.C() { // closes when the old memory is retired
		n++
	}
	if n != 2 {
		t.Fatalf("subscriber got %d entries", n)
	}
}

func TestUnsafeRunesEscaped(t *testing.T) {
	in := "a\u009b31mb\u202eevil"
	for _, enc := range []Encoder{TextEncoder{}, JSONEncoder{}, PrettyEncoder{}} {
		b := enc.Encode(nil, &Record{Time: time.Now(), Msg: in, Attrs: []slog.Attr{slog.String("k", in)}})
		if strings.ContainsRune(string(b), 0x9b) || strings.ContainsRune(string(b), 0x202e) {
			t.Fatalf("%T wrote %q", enc, b)
		}
	}
}

func TestLevelNames(t *testing.T) {
	for l, want := range map[slog.Level]string{slog.LevelInfo: "INFO", slog.LevelInfo + 2: "INFO+2", LevelTrace - 1: "TRACE-1", slog.LevelError + 4: "ERROR+4", slog.LevelDebug: "DEBUG"} {
		if got := LevelName(l); got != want {
			t.Fatalf("%d: %s, want %s", l, got, want)
		}
	}
}

func TestAsyncReportsDrops(t *testing.T) {
	block := make(chan struct{})
	o := &blockingOut{block: block}
	a := Async(o, 1)
	var dropped uint64
	done := make(chan struct{})
	a.OnDrop = func(n uint64) { dropped += n; close(done) }
	for range 10 {
		_ = a.Write([]byte("x"))
	}
	close(block)
	<-done
	_ = a.Close()
	if dropped == 0 || dropped != a.Dropped() {
		t.Fatalf("reported %d of %d", dropped, a.Dropped())
	}
}

type blockingOut struct{ block chan struct{} }

func (b *blockingOut) Write([]byte) error { <-b.block; return nil }
func (b *blockingOut) Close() error       { return nil }

func TestCapturedSourceIsCaller(t *testing.T) {
	l, o := withRoute(t, Config{AddSource: true}, nil)
	StdLogger(l.Module("x"), slog.LevelInfo).Print("hello")
	if !strings.Contains(o.all(), "review_test.go:") {
		t.Fatalf("got %q", o.all())
	}
}
