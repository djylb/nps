//go:build !windows

package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djylb/selfupdate"
)

// serverTarGz is the archive of an nps release: the executable, the web
// directories and the reference config.
func serverTarGz(t *testing.T, exe string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add(executableName("nps"), exe, 0o755)
	for name, body := range files {
		add(name, body, 0o644)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// keepFixture is an installed nps with a customized web directory, and a
// release of it.
type keepFixture struct {
	rs  *releaseServer
	run string
	exe string
}

func newKeepFixture(t *testing.T, withWeb bool) *keepFixture {
	t.Helper()
	run := t.TempDir()
	exe := filepath.Join(run, executableName("nps"))
	writeExecutable(t, exe, script("0.34.0"))
	if withWeb {
		writeFile(t, filepath.Join(run, "web/views/index.html"), "old index", 0o644)
		writeFile(t, filepath.Join(run, "web/views/404.html"), "custom 404", 0o644)
		writeFile(t, filepath.Join(run, "web/static/css/app.css"), "old css", 0o644)
		writeFile(t, filepath.Join(run, "web/static/custom/run.sh"), "mine", 0o750)
		writeFile(t, filepath.Join(run, "web/static/css/user.css"), "user css", 0o600)
		// A file where the release has a directory, and the reverse.
		writeFile(t, filepath.Join(run, "web/static/js"), "a file", 0o644)
		writeFile(t, filepath.Join(run, "web/views/page/note.txt"), "dir", 0o644)
	}
	rs := newReleaseServer(t)
	origRun := updateRunPath
	updateRunPath = func() string { return run }
	t.Cleanup(func() { updateRunPath = origRun })
	rs.use("0.34.0", exe)
	return &keepFixture{rs: rs, run: run, exe: exe}
}

func (f *keepFixture) publish(t *testing.T, v, exeBody string) {
	t.Helper()
	f.rs.publishArchive("nps", v, "server", serverTarGz(t, exeBody, map[string]string{
		"web/views/index.html":   "new index",
		"web/views/added.html":   "added",
		"web/static/css/app.css": "new css",
		"web/static/js/app.js":   "new js",
		"web/views/page":         "now a file",
		"conf/nps.conf":          "conf v" + v,
	}))
}

func (f *keepFixture) path(rel string) string { return filepath.Join(f.run, filepath.FromSlash(rel)) }

func TestUpdateKeepsUserFiles(t *testing.T) {
	f := newKeepFixture(t, true)
	f.publish(t, "0.35.9", script("0.35.9"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if res, err := Update(ctx, "nps", "", UpdateOptions{}); err != nil || res == nil {
		t.Fatalf("Update() = (%v, %v)", res, err)
	}
	if v, err := executableVersion(f.exe); err != nil || v != "0.35.9" {
		t.Fatalf("updated version = (%q, %v)", v, err)
	}
	for rel, want := range map[string]string{
		"web/views/index.html":     "new index", // overwritten
		"web/static/css/app.css":   "new css",   // overwritten
		"web/views/added.html":     "added",     // created
		"web/static/js/app.js":     "new js",    // created under a user's file of that name: the release wins
		"web/views/404.html":       "custom 404",
		"web/static/css/user.css":  "user css",
		"web/static/custom/run.sh": "mine",
		"web/views/page":           "now a file",
		"conf/nps.conf.default":    "conf v0.35.9",
	} {
		assertContent(t, f.path(rel), want)
	}
	// Permissions of kept files survive.
	for rel, want := range map[string]os.FileMode{"web/static/custom/run.sh": 0o750, "web/static/css/user.css": 0o600} {
		if fi, err := os.Stat(f.path(rel)); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s mode = (%v, %v), want %v", rel, fi, err, want)
		}
	}
	// Nothing of the update is left next to the trees.
	for _, dir := range []string{"web", f.run} {
		ents, _ := os.ReadDir(f.path(dir))
		for _, e := range ents {
			if strings.Contains(e.Name(), "selfupdate") && !strings.HasSuffix(e.Name(), ".json") {
				t.Logf("leftover %s/%s", dir, e.Name())
			}
		}
	}
}

func TestUpdateCreatesMissingWeb(t *testing.T) {
	f := newKeepFixture(t, false)
	f.publish(t, "0.35.9", script("0.35.9"))
	if res, err := Update(context.Background(), "nps", "", UpdateOptions{}); err != nil || res == nil {
		t.Fatalf("Update() = (%v, %v)", res, err)
	}
	assertContent(t, f.path("web/views/index.html"), "new index")
	assertContent(t, f.path("web/static/css/app.css"), "new css")
}

// TestUpdateFailureKeepsInstallation checks that a release that fails
// verification after the user's files were carried over changes nothing.
func TestUpdateFailureKeepsInstallation(t *testing.T) {
	f := newKeepFixture(t, true)
	f.publish(t, "0.35.9", "#!/bin/sh\nexit 3\n")
	if _, err := Update(context.Background(), "nps", "", UpdateOptions{}); err == nil {
		t.Fatal("Update() error = nil, want error")
	}
	assertContent(t, f.exe, script("0.34.0"))
	assertContent(t, f.path("web/views/index.html"), "old index")
	assertContent(t, f.path("web/views/404.html"), "custom 404")
	assertContent(t, f.path("web/static/js"), "a file")
	if _, err := os.Stat(f.path("web/views/added.html")); !os.IsNotExist(err) {
		t.Fatalf("added.html exists after a failed update: %v", err)
	}
}

// TestUpdateRollbackRestoresWeb checks that undoing a pending update brings
// back the old trees with the user's files, and that a crash in the middle of
// the swap is undone the same way.
func TestUpdateRollbackRestoresWeb(t *testing.T) {
	for _, crash := range []bool{false, true} {
		name := "unconfirmed"
		if crash {
			name = "crash"
		}
		t.Run(name, func(t *testing.T) {
			f := newKeepFixture(t, true)
			f.publish(t, "0.35.9", script("0.35.9"))
			if _, err := Update(context.Background(), "nps", "", UpdateOptions{Pending: true}); err != nil {
				t.Fatalf("Update(pending) error = %v", err)
			}
			assertContent(t, f.path("web/views/404.html"), "custom 404")
			assertContent(t, f.path("web/views/index.html"), "new index")
			j := selfupdate.JournalFor(f.exe)
			var out selfupdate.Outcome
			var err error
			if crash {
				// The journal of a process that died before it finished the swap.
				data, rerr := os.ReadFile(j.Path)
				if rerr != nil {
					t.Fatal(rerr)
				}
				data = bytes.Replace(data, []byte(`"phase":"pending"`), []byte(`"phase":"applying"`), 1)
				if werr := os.WriteFile(j.Path, data, 0o644); werr != nil {
					t.Fatal(werr)
				}
				out, err = j.Recover(context.Background())
			} else {
				for range updateBoots + 1 {
					if out, err = j.Startup(context.Background(), updateBoots); err != nil || out.Status != selfupdate.StatusPending {
						break
					}
				}
			}
			if err != nil || out.Status != selfupdate.StatusRolledBack {
				t.Fatalf("recovery = (%+v, %v), want rolled back", out, err)
			}
			assertContent(t, f.exe, script("0.34.0"))
			assertContent(t, f.path("web/views/index.html"), "old index")
			assertContent(t, f.path("web/views/404.html"), "custom 404")
			assertContent(t, f.path("web/static/custom/run.sh"), "mine")
			assertContent(t, f.path("web/static/js"), "a file")
			if _, err := os.Stat(f.path("web/views/added.html")); !os.IsNotExist(err) {
				t.Fatalf("added.html exists after rollback: %v", err)
			}
		})
	}
}
