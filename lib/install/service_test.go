package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kardianos/service"
)

type fakeService struct {
	service.Service
	stop    func() error
	started bool
}

func (f *fakeService) Stop() error { return f.stop() }

func (f *fakeService) Start() error {
	f.started = true
	return nil
}

func TestRestartWithTimeout(t *testing.T) {
	s := &fakeService{stop: func() error { return nil }}
	if err := restartWithTimeout(s, "Npc", time.Second); err != nil || !s.started {
		t.Fatalf("restart = %v, started = %v; want nil, true", err, s.started)
	}

	block := make(chan struct{})
	defer close(block)
	s = &fakeService{stop: func() error { <-block; return nil }}
	err := restartWithTimeout(s, "Npc", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not stop") || s.started {
		t.Fatalf("restart of a hanging stop = %v, started = %v; want a timeout error and no start", err, s.started)
	}
}

func TestIsExecutableOf(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, executableName("npc"))
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, executableName("nps"))
	if err := os.WriteFile(other, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want bool
	}{
		{exe, true},
		{other, false},
		{filepath.Join(dir, executableName("missing")), false},
		{dir, false},
	} {
		if got := isExecutableOf(c.path, "npc"); got != c.want {
			t.Errorf("isExecutableOf(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestRenameFileMissingSourceFailsAtOnce(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	if err := renameFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dest")); err == nil {
		t.Fatal("renameFile of a missing file succeeded")
	}
	if d := time.Since(start); d > renameRetry/2 {
		t.Fatalf("renameFile of a missing file took %v", d)
	}
}

func TestHelperSleep(t *testing.T) {
	if os.Getenv("NPS_TEST_SLEEP") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

func TestProcessesRunning(t *testing.T) {
	exe, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestHelperSleep$")
	cmd.Env = append(os.Environ(), "NPS_TEST_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	pid := int32(cmd.Process.Pid)
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(processesRunning([]string{exe}), pid) {
		if time.Now().After(deadline) {
			t.Fatalf("processesRunning(%q) misses helper %d", exe, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if slices.Contains(processesRunning([]string{exe}), int32(os.Getpid())) {
		t.Fatal("processesRunning lists the current process")
	}
	if got := processesRunning([]string{filepath.Join(t.TempDir(), "none")}); len(got) != 0 {
		t.Fatalf("processesRunning(missing) = %v", got)
	}
}
