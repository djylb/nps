package logs

import (
	"strings"
	"testing"
)

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
