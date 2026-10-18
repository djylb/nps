package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestHelperRestart is run as a child process by the tests below.
func TestHelperRestart(t *testing.T) {
	out := os.Getenv("NPS_TEST_OUT")
	if out == "" {
		t.Skip("helper process")
	}
	exe, err := currentExecutable()
	if err != nil {
		os.Exit(2)
	}
	switch os.Getenv("NPS_TEST_STAGE") {
	case "relaunch":
		_ = os.Setenv("NPS_TEST_STAGE", "write")
		_ = Relaunch(exe)
		os.Exit(3)
	case "write":
		_ = os.WriteFile(out, []byte(strconv.Itoa(os.Getpid())), 0o644)
		os.Exit(0)
	}
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	t.Fatalf("%s was not written", path)
	return ""
}

func helperCommand(t *testing.T, stage, out string) *exec.Cmd {
	t.Helper()
	exe, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestHelperRestart$")
	cmd.Env = append(os.Environ(), "NPS_TEST_OUT="+out, "NPS_TEST_STAGE="+stage)
	return cmd
}

func TestRelaunch(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pid")
	cmd := helperCommand(t, "relaunch", out)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitForFile(t, out)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("relaunching process: %v", err)
	}
	if runtime.GOOS != "windows" && pid != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("relaunched as process %s, want the same process %d", pid, cmd.Process.Pid)
	}
}

func TestExecutableVersionRejectsUnrunnable(t *testing.T) {
	p := filepath.Join(t.TempDir(), executableName("npc"))
	if err := os.WriteFile(p, []byte("not an executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v, err := executableVersion(p); err == nil {
		t.Fatalf("executableVersion of a non-executable = %q, want an error", v)
	}
}
