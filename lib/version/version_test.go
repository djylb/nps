package version

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestGetVersion(t *testing.T) {
	if got := GetVersion(0); got != MinVersions[0] {
		t.Fatalf("GetVersion(0) = %q, want %q", got, MinVersions[0])
	}

	latest := GetLatest()
	for _, idx := range []int{-1, len(MinVersions)} {
		if got := GetVersion(idx); got != latest {
			t.Fatalf("GetVersion(%d) = %q, want latest %q", idx, got, latest)
		}
	}
}

func TestVersionRangeHelpers(t *testing.T) {
	if got := GetCount(); got != len(MinVersions) {
		t.Fatalf("GetCount() = %d, want %d", got, len(MinVersions))
	}

	if got := GetLatest(); got != MinVersions[len(MinVersions)-1] {
		t.Fatalf("GetLatest() = %q, want %q", got, MinVersions[len(MinVersions)-1])
	}

	if got := GetLatestIndex(); got != len(MinVersions)-1 {
		t.Fatalf("GetLatestIndex() = %d, want %d", got, len(MinVersions)-1)
	}
}

func TestVersionRangeHelpers_EmptyMinVersions(t *testing.T) {
	origin := MinVersions
	defer func() { MinVersions = origin }()

	MinVersions = nil

	if got := GetCount(); got != 0 {
		t.Fatalf("GetCount() = %d, want 0", got)
	}
	if got := GetLatest(); got != "" {
		t.Fatalf("GetLatest() = %q, want empty string", got)
	}
	if got := GetLatestIndex(); got != 0 {
		t.Fatalf("GetLatestIndex() = %d, want 0", got)
	}
}

func TestGetMinVersion(t *testing.T) {
	if got := GetMinVersion(true); got != MinVersions[MinVer] {
		t.Fatalf("GetMinVersion(true) = %q, want %q", got, MinVersions[MinVer])
	}
	if got := GetMinVersion(false); got != MinVersions[0] {
		t.Fatalf("GetMinVersion(false) = %q, want %q", got, MinVersions[0])
	}
}

func TestGetIndex(t *testing.T) {
	if got := GetIndex(MinVersions[2]); got != 2 {
		t.Fatalf("GetIndex(existing) = %d, want 2", got)
	}
	if got := GetIndex("not-exist"); got != -1 {
		t.Fatalf("GetIndex(non-existing) = %d, want -1", got)
	}
}

func TestPrintVersion(t *testing.T) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdout = w

	PrintVersion(1)

	if err := w.Close(); err != nil {
		t.Fatalf("close writer error: %v", err)
	}
	os.Stdout = origStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}

	content := string(out)
	if !strings.Contains(content, "Version: "+VERSION) {
		t.Fatalf("PrintVersion() output missing version: %q", content)
	}
	if !strings.Contains(content, "Core version: "+GetVersion(1)) {
		t.Fatalf("PrintVersion() output missing core version: %q", content)
	}
}
