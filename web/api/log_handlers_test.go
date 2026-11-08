package api

import (
	"log/slog"
	"testing"

	"github.com/djylb/slogx"
)

func TestLogHandlers(t *testing.T) {
	if err := slogx.Default.Configure(slogx.Config{Console: slogx.ConsoleOff, Level: "info", Memory: 10}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slogx.Default.Configure(slogx.Config{}) })
	m := slogx.Module("web")
	m.Info("one", "k", 1)
	m.Warn("two")
	slogx.Module("bridge").Warn("three")

	a := &App{}
	entries := func(v map[string]string) map[string]any {
		c := &liveContext{rateResponseContext: rateResponseContext{rateInputContext: rateInputContext{values: v}}}
		a.LogEntries(c)
		return c.response.(map[string]any)
	}
	all := entries(map[string]string{})
	if got := len(all["entries"].([]slogx.Entry)); got != 3 {
		t.Fatalf("%d entries", got)
	}
	warn := entries(map[string]string{"level": "warn", "module": "web"})
	if es := warn["entries"].([]slogx.Entry); len(es) != 1 || es[0].Msg != "two" {
		t.Fatalf("filtered %+v", es)
	}
	after := entries(map[string]string{"after": "2"})
	if es := after["entries"].([]slogx.Entry); len(es) != 1 || es[0].Msg != "three" || after["seq"].(uint64) != 3 {
		t.Fatalf("after %+v", after)
	}

	override := func(v map[string]string) int {
		c := &liveContext{rateResponseContext: rateResponseContext{rateInputContext: rateInputContext{values: v}}}
		a.LogOverride(c)
		return c.status
	}
	if s := override(map[string]string{"module": "p2p", "level": "debug", "ttl": "60"}); s != 200 || slogx.Default.Levels().Of("p2p") != slog.LevelDebug {
		t.Fatalf("override: %d", s)
	}
	if s := override(map[string]string{"module": "p2p", "ttl": "0"}); s != 200 || slogx.Default.Levels().Of("p2p") != slog.LevelInfo {
		t.Fatalf("clear: %d", s)
	}
	if s := override(map[string]string{"module": "p2p", "level": "loud", "ttl": "60"}); s != 400 {
		t.Fatalf("bad level: %d", s)
	}
}
