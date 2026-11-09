package runenv

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

	ConfPath = filepath.Join(t.TempDir(), "custom-nps")
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
	abs, err := filepath.Abs(filepath.Join(".", "nps.log"))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
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
	oldStart := startTime
	defer func() { startTime = oldStart }()

	startTime = time.Now().Add(-(24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second))

	runtimeText := GetRunTime()
	for _, token := range []string{"1d", "2h", "3m", "4s"} {
		if !strings.Contains(runtimeText, token) {
			t.Fatalf("GetRunTime() = %q, missing %q", runtimeText, token)
		}
	}

	minInt := int64(24*3600 + 2*3600 + 3*60 + 4)
	if got := GetRunSecs(); got < minInt {
		t.Fatalf("GetRunSecs() = %d, want >= %d", got, minInt)
	}

	if got := GetStartTime(); got != startTime.Unix() {
		t.Fatalf("GetStartTime() = %d, want %d", got, startTime.Unix())
	}
}

func TestRunTimeZeroValueShowsSeconds(t *testing.T) {
	oldStart := startTime
	defer func() { startTime = oldStart }()

	startTime = time.Now()
	if got := GetRunTime(); !strings.Contains(got, "s") {
		t.Fatalf("GetRunTime() = %q, want seconds suffix", got)
	}
}

func TestLogAndTmpPaths(t *testing.T) {
	if IsWindows() {
		appPath := GetAppPath()
		if got := GetLogPath(); got != filepath.Join(appPath, "nps.log") {
			t.Fatalf("GetLogPath() = %q, want %q", got, filepath.Join(appPath, "nps.log"))
		}
		if got := GetNpcLogPath(); got != filepath.Join(appPath, "npc.log") {
			t.Fatalf("GetNpcLogPath() = %q, want %q", got, filepath.Join(appPath, "npc.log"))
		}
		if got := GetConfigPath(); got != filepath.Join(appPath, "conf/npc.jsonc") {
			t.Fatalf("GetConfigPath() = %q, want %q", got, filepath.Join(appPath, "conf/npc.jsonc"))
		}
		return
	}

	if got := GetLogPath(); got != "/var/log/nps.log" {
		t.Fatalf("GetLogPath() = %q, want %q", got, "/var/log/nps.log")
	}
	if got := GetNpcLogPath(); got != "/var/log/npc.log" {
		t.Fatalf("GetNpcLogPath() = %q, want %q", got, "/var/log/npc.log")
	}
	if got := GetConfigPath(); got != "conf/npc.jsonc" {
		t.Fatalf("GetConfigPath() = %q, want %q", got, "conf/npc.jsonc")
	}
}

func TestConfigPathFallsBackToExistingConf(t *testing.T) {
	if IsWindows() {
		t.Skip("relative to the application directory")
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("conf", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("conf/npc.conf", []byte("[common]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GetConfigPath(); got != "conf/npc.conf" {
		t.Fatalf("GetConfigPath() = %q, want conf/npc.conf", got)
	}
	if err := os.WriteFile("conf/npc.jsonc", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GetConfigPath(); got != "conf/npc.jsonc" {
		t.Fatalf("GetConfigPath() = %q, want conf/npc.jsonc", got)
	}
}

func TestPathAndFileHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(f, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !FileExists(f) || FileExists(filepath.Join(tmpDir, "missing.txt")) {
		t.Fatal("FileExists() returned unexpected result")
	}
	if got, err := os.ReadFile(f); err != nil || string(got) != "abc" {
		t.Fatalf("ReadAllFromFile() = (%q, %v), want (abc, nil)", string(got), err)
	}

	oldConf := ConfPath
	oldArgs := append([]string(nil), os.Args...)
	defer func() { ConfPath = oldConf; os.Args = oldArgs }()
	ConfPath = tmpDir
	os.Args = []string{"nps", "-c", "conf/nps.conf"}

	if got := GetPath("conf/a.conf"); !strings.HasSuffix(got, filepath.Join("conf", "a.conf")) {
		t.Fatalf("GetPath(relative) = %q, want suffix conf/a.conf", got)
	}
}
