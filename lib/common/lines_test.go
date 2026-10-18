package common

import (
	"reflect"
	"testing"
)

func TestSplitLines(t *testing.T) {
	want := []string{"a", "b", "", "c"}
	for _, s := range []string{"a\nb\n\nc", "a\r\nb\r\n\r\nc", "a\rb\r\rc", "\uFEFFa\r\nb\n\rc"} {
		if got := SplitLines(s); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitLines(%q) = %q, want %q", s, got, want)
		}
	}
	if got := NormalizeNewlines("x\r\ny\rz"); got != "x\ny\nz" {
		t.Errorf("NormalizeNewlines = %q", got)
	}
}
