package logs

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func resetTestLoggerState() {
	bufferWriter = nil
	Logger = zerolog.Nop()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}

func TestNewBufferWriterUsesDefaultCapacityForInvalidInput(t *testing.T) {
	w := NewBufferWriter(0)

	if w.cap != defaultBufSize {
		t.Fatalf("unexpected capacity, got %d want %d", w.cap, defaultBufSize)
	}
}

func TestBufferWriterWriteKeepsCapacityOnOverflow(t *testing.T) {
	w := NewBufferWriter(16)

	if _, err := w.Write([]byte("1234567890")); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if _, err := w.Write([]byte("abcdefghij")); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	got := w.GetAndClear()
	want := "567890abcdefghij"
	if got != want {
		t.Fatalf("unexpected buffer content, got %q want %q", got, want)
	}
}

func TestBufferWriterWriteWithLargePayload(t *testing.T) {
	w := NewBufferWriter(16)
	large := strings.Repeat("x", 64)

	if _, err := w.Write([]byte(large)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got := w.GetAndClear()
	want := strings.Repeat("x", 16)
	if got != want {
		t.Fatalf("unexpected buffer content length=%d, want length=%d", len(got), len(want))
	}
}

func TestBufferWriterGetAndClearResetsState(t *testing.T) {
	w := NewBufferWriter(16)
	large := strings.Repeat("x", 128)

	if _, err := w.Write([]byte(large)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_ = w.GetAndClear()
	if w.start != 0 || w.size != 0 {
		t.Fatalf("buffer state should be reset, start=%d size=%d", w.start, w.size)
	}
	if gotCap := len(w.buf); gotCap != 16 {
		t.Fatalf("buffer capacity should remain fixed, got=%d", gotCap)
	}
}

func TestEnableInMemoryBufferAndGetBufferedLogs(t *testing.T) {
	resetTestLoggerState()
	t.Cleanup(resetTestLoggerState)

	EnableInMemoryBuffer(32)
	if bufferWriter == nil {
		t.Fatalf("buffer writer should be initialized")
	}

	Init("off", "info", "", 1, 1, 1, false, false)
	Info("hello")

	firstRead := GetBufferedLogs()
	if !strings.Contains(firstRead, "hello") {
		t.Fatalf("expected buffered logs to contain message, got %q", firstRead)
	}

	secondRead := GetBufferedLogs()
	if secondRead != "" {
		t.Fatalf("expected second read to be empty, got %q", secondRead)
	}
}

func TestSetLevelIgnoresInvalidLevel(t *testing.T) {
	resetTestLoggerState()
	t.Cleanup(resetTestLoggerState)

	SetLevel("debug")
	if got := zerolog.GlobalLevel(); got != zerolog.DebugLevel {
		t.Fatalf("unexpected level after valid update, got %s", got)
	}

	SetLevel("invalid-level")
	if got := zerolog.GlobalLevel(); got != zerolog.DebugLevel {
		t.Fatalf("invalid level should not update global level, got %s", got)
	}
}

func TestZapAdapterWriteRoutesLevels(t *testing.T) {
	resetTestLoggerState()
	t.Cleanup(resetTestLoggerState)

	EnableInMemoryBuffer(1024)
	Init("off", "debug", "", 1, 1, 1, false, false)

	adapter := zapAdapter{}
	cases := []string{
		"DEBUG\tdebug msg\n",
		"INFO\tinfo msg\n",
		"WARN\twarn msg\n",
		"WARNING\twarning msg\n",
		"ERROR\terror msg\n",
		"UNKNOWN\tfallback msg\n",
		"message without tab\n",
	}

	for _, c := range cases {
		if _, err := adapter.Write([]byte(c)); err != nil {
			t.Fatalf("write failed for %q: %v", c, err)
		}
	}

	logs := GetBufferedLogs()
	for _, msg := range []string{"debug msg", "info msg", "warn msg", "warning msg", "error msg", "fallback msg", "message without tab"} {
		if !strings.Contains(logs, msg) {
			t.Fatalf("expected logs to include %q, got %q", msg, logs)
		}
	}
}
