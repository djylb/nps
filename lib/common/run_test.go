package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGetInstallPathWithConfPath(t *testing.T) {
	oldConf := ConfPath
	defer func() { ConfPath = oldConf }()

	ConfPath = "/tmp/custom-nps"
	if got := GetInstallPath(); got != ConfPath {
		t.Fatalf("GetInstallPath() = %q, want %q", got, ConfPath)
	}
}

func TestGetRunPathUsesInstallPathWhenExists(t *testing.T) {
	oldConf := ConfPath
	defer func() { ConfPath = oldConf }()
	oldArgs := append([]string(nil), os.Args...)
	defer func() { os.Args = oldArgs }()

	tmp := t.TempDir()
	ConfPath = tmp
	os.Args = []string{"nps", "-c", "conf/nps.conf"}

	if got := GetRunPath(); got != tmp {
		t.Fatalf("GetRunPath() = %q, want %q", got, tmp)
	}
}

func TestGetRunPathFallsBackToAppPathWhenInstallPathMissing(t *testing.T) {
	oldConf := ConfPath
	defer func() { ConfPath = oldConf }()
	oldArgs := append([]string(nil), os.Args...)
	defer func() { os.Args = oldArgs }()

	ConfPath = filepath.Join(t.TempDir(), "not-exist")
	os.Args = []string{"nps", "-c", "conf/nps.conf"}

	want := GetAppPath()
	if got := GetRunPath(); got != want {
		t.Fatalf("GetRunPath() = %q, want %q", got, want)
	}
}

func TestResolvePath(t *testing.T) {
	abs := "/var/log/nps.log"
	if got := ResolvePath(abs); got != abs {
		t.Fatalf("ResolvePath() absolute = %q, want %q", got, abs)
	}

	oldConf := ConfPath
	defer func() { ConfPath = oldConf }()
	oldArgs := append([]string(nil), os.Args...)
	defer func() { os.Args = oldArgs }()

	runPath := t.TempDir()
	ConfPath = runPath
	os.Args = []string{"nps", "-c", "conf/nps.conf"}

	rel := "conf/nps.conf"
	want := filepath.Join(runPath, rel)
	if got := ResolvePath(rel); got != want {
		t.Fatalf("ResolvePath() relative = %q, want %q", got, want)
	}
}

func TestRunTimeAndSecs(t *testing.T) {
	oldStart := StartTime
	defer func() { StartTime = oldStart }()

	StartTime = time.Now().Add(-(24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second))

	runtimeText := GetRunTime()
	for _, token := range []string{"1d", "2h", "3m", "4s"} {
		if !strings.Contains(runtimeText, token) {
			t.Fatalf("GetRunTime() = %q, missing %q", runtimeText, token)
		}
	}

	min := int64(24*3600 + 2*3600 + 3*60 + 4)
	if got := GetRunSecs(); got < min {
		t.Fatalf("GetRunSecs() = %d, want >= %d", got, min)
	}

	if got := GetStartTime(); got != StartTime.Unix() {
		t.Fatalf("GetStartTime() = %d, want %d", got, StartTime.Unix())
	}
}
