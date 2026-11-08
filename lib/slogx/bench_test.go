package slogx

import (
	"io"
	"log/slog"
	"testing"
)

type discard struct{}

func (discard) Write([]byte) error { return nil }
func (discard) Close() error       { return nil }

func benchLogs(b *testing.B, enc Encoder) *Logs {
	l := New()
	if err := l.Configure(Config{Output: "off", Level: "info", Routes: []Route{{Encoder: enc, Output: discard{}}}}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(l.Close)
	return l
}

var benchDef = Def{Name: "bench_ev", Level: slog.LevelInfo}

func BenchmarkDisabled(b *testing.B) {
	m := benchLogs(b, nil).Module("x")
	b.ReportAllocs()
	for b.Loop() {
		m.Debug("hidden", "k", 1, "s", "v")
	}
}

func BenchmarkText(b *testing.B) {
	m := benchLogs(b, TextEncoder{}).Module("x")
	b.ReportAllocs()
	for b.Loop() {
		m.Info("request done", "client", 42, "route", "tcp:8080", "dur", 1500, "ok", true)
	}
}

func BenchmarkJSON(b *testing.B) {
	m := benchLogs(b, JSONEncoder{}).Module("x")
	b.ReportAllocs()
	for b.Loop() {
		m.Info("request done", "client", 42, "route", "tcp:8080", "dur", 1500, "ok", true)
	}
}

func BenchmarkEmit(b *testing.B) {
	m := benchLogs(b, TextEncoder{}).Module("x")
	b.ReportAllocs()
	for b.Loop() {
		Emit(m, &benchDef, "src_ip", "192.0.2.1", "reason", "bad_key")
	}
}

func BenchmarkWithAttrs(b *testing.B) {
	m := benchLogs(b, TextEncoder{}).Module("x").With("session", "abcd1234")
	b.ReportAllocs()
	for b.Loop() {
		m.Info("frame", "n", 1)
	}
}

func BenchmarkStdTextHandler(b *testing.B) {
	m := slog.New(slog.NewTextHandler(io.Discard, nil))
	b.ReportAllocs()
	for b.Loop() {
		m.Info("request done", "client", 42, "route", "tcp:8080", "dur", 1500, "ok", true)
	}
}

func TestEnabledLineDoesNotAllocate(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	l := New()
	if err := l.Configure(Config{Output: "off", Routes: []Route{{Output: discard{}}}}); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	m := l.Module("x")
	if n := testing.AllocsPerRun(1000, func() { m.Info("done", "client", 42, "route", "tcp:8080", "ok", true) }); n != 0 {
		t.Fatalf("%v allocations per line", n)
	}
	if n := testing.AllocsPerRun(1000, func() { Emit(m, &benchDef, "src_ip", "192.0.2.1") }); n != 0 {
		t.Fatalf("%v allocations per event", n)
	}
}
