//go:build windows

package install

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStartDetached(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pid")
	cmd := helperCommand(t, "write", out)
	for _, kv := range cmd.Env[len(cmd.Env)-2:] {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	p, err := startDetached(cmd.Path, cmd.Args[1:]...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = p.Wait() }()
	if got := waitForFile(t, out); got != strconv.Itoa(p.Pid) {
		t.Fatalf("helper wrote pid %s, want %d", got, p.Pid)
	}
}
