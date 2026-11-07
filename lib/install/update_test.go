//go:build !windows

package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/djylb/selfupdate"
)

// releaseServer serves a signed manifest and its archives the way the release
// mirror does: the GitHub location of the manifest is missing, so the update
// falls back to the mirror, which maps the release URLs of the artifacts to
// the tag of the mirror repository.
type releaseServer struct {
	t     *testing.T
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	files map[string][]byte
	srv   *httptest.Server
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	pub, priv, err := selfupdate.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	rs := &releaseServer{t: t, pub: pub, priv: priv, files: map[string][]byte{}}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := rs.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

// script is an executable that reports version v the way npc -version does.
func script(v string) string {
	return "#!/bin/sh\necho 'Version: " + v + "'\n"
}

func tarGzOf(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// publish signs a manifest listing release v of npc, whose archive holds
// body, as scripts/release_manifest.sh does. tamper changes the archive after
// it was listed.
func (rs *releaseServer) publish(v, body string, tamper bool) {
	rs.t.Helper()
	archive := tarGzOf(rs.t, "npc", body)
	file := runtime.GOOS + "_" + runtime.GOARCH + "_client.tar.gz"
	sum := sha256.Sum256(archive)
	m := &selfupdate.Manifest{
		Format:    selfupdate.ManifestFormat,
		Serial:    uint64(time.Now().UnixNano() / 1000),
		Product:   releaseProduct,
		Published: time.Now().Add(-time.Minute),
		Expires:   time.Now().Add(time.Hour),
		Baseline:  []selfupdate.Rule{},
		Releases: []selfupdate.Release{{Version: v, Artifacts: []selfupdate.Artifact{{
			Name: artifactName("npc"), OS: runtime.GOOS, Arch: runtime.GOARCH,
			URL:    githubReleases + "v" + v + "/" + file,
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(archive)),
			Format: selfupdate.FormatTarGzip,
		}}}},
	}
	signed, err := selfupdate.SignManifest(m, rs.priv)
	if err != nil {
		rs.t.Fatal(err)
	}
	if tamper {
		archive = tarGzOf(rs.t, "npc", body+"#")
	}
	rs.files = map[string][]byte{
		"/gh/nps@manifest-channel/manifest.json": signed,
		"/gh/nps-mirror@v" + v + "/" + file:      archive,
	}
}

// use makes Update read the release server, run as version current and
// replace targets.
func (rs *releaseServer) use(current string, targets ...string) {
	origUpdater, origTargets := newUpdater, updateTargets
	rs.t.Cleanup(func() { newUpdater, updateTargets = origUpdater, origTargets })
	newUpdater = func(bin string, force bool) (*selfupdate.Updater, error) {
		u, err := releaseUpdater(bin, force)
		if err != nil {
			return nil, err
		}
		base := rs.srv.URL
		u.Sources = []selfupdate.Source{
			&selfupdate.HTTPSource{URL: base + "/releases/download/channel/manifest.json"},
			&mirrorSource{selfupdate.HTTPSource{URL: base + "/gh/nps@manifest-channel/manifest.json", Base: base + "/gh/"}},
		}
		u.Trust = selfupdate.Trust{Keys: []selfupdate.TrustedKey{{Public: rs.pub}}}
		u.Current = current
		return u, nil
	}
	updateTargets = func(string, string) ([]string, error) { return targets, nil }
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = (%q, %v), want %q", path, got, err, want)
	}
}

func TestUpdateReplacesExecutable(t *testing.T) {
	rs := newReleaseServer(t)
	rs.publish("0.35.9", script("0.35.9"), false)
	target := filepath.Join(t.TempDir(), "npc")
	writeExecutable(t, target, script("0.34.0"))
	rs.use("0.34.0", target)

	res, err := Update(context.Background(), "npc", "", UpdateOptions{})
	if err != nil || res == nil {
		t.Fatalf("Update() = (%v, %v)", res, err)
	}
	if res.Version != "0.35.9" || len(res.Replaced) != 1 || res.Replaced[0] != target {
		t.Fatalf("Update() = %+v", res)
	}
	if v, err := executableVersion(target); err != nil || v != "0.35.9" {
		t.Fatalf("updated version = (%q, %v)", v, err)
	}

	// The running version is now the latest: nothing to do, unless forced.
	rs.use("0.35.9", target)
	if res, err := Update(context.Background(), "npc", "", UpdateOptions{}); res != nil || err != nil {
		t.Fatalf("Update(latest) = (%v, %v), want (nil, nil)", res, err)
	}
	if res, err := Update(context.Background(), "npc", "", UpdateOptions{Force: true}); err != nil || res == nil || res.Version != "0.35.9" {
		t.Fatalf("Update(latest, force) = (%v, %v)", res, err)
	}
}

func TestUpdateRefusesBadArtifact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		tamper bool
	}{
		{name: "digest", body: script("0.35.9"), tamper: true},
		{name: "wrong version", body: script("0.35.8")},
		{name: "does not run", body: "#!/bin/sh\nexit 3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := newReleaseServer(t)
			rs.publish("0.35.9", tc.body, tc.tamper)
			target := filepath.Join(t.TempDir(), "npc")
			old := script("0.34.0")
			writeExecutable(t, target, old)
			rs.use("0.34.0", target)
			if res, err := Update(context.Background(), "npc", "", UpdateOptions{}); err == nil {
				t.Fatalf("Update() = %+v, want error", res)
			}
			assertContent(t, target, old)
		})
	}
}

// TestUpdateSetIsAtomic checks that a target that cannot be replaced undoes
// the replacement of the others.
func TestUpdateSetIsAtomic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	rs := newReleaseServer(t)
	rs.publish("0.35.9", script("0.35.9"), false)
	old := script("0.34.0")
	first := filepath.Join(t.TempDir(), "npc")
	writeExecutable(t, first, old)
	lockedDir := t.TempDir()
	second := filepath.Join(lockedDir, "npc")
	writeExecutable(t, second, old)
	if err := os.Chmod(lockedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })
	rs.use("0.34.0", first, second)

	_, err := Update(context.Background(), "npc", "", UpdateOptions{})
	if err == nil {
		t.Fatal("Update() error = nil, want error")
	}
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "as root") {
		t.Fatalf("Update() error = %v, want a permission error naming root", err)
	}
	assertContent(t, first, old)
	assertContent(t, second, old)
}

// TestUpdatePendingRollsBack checks that a pending update that never confirms
// itself is rolled back after updateBoots starts.
func TestUpdatePendingRollsBack(t *testing.T) {
	rs := newReleaseServer(t)
	rs.publish("0.35.9", script("0.35.9"), false)
	target := filepath.Join(t.TempDir(), "npc")
	old := script("0.34.0")
	writeExecutable(t, target, old)
	rs.use("0.34.0", target)

	if _, err := Update(context.Background(), "npc", "", UpdateOptions{Pending: true}); err != nil {
		t.Fatalf("Update(pending) error = %v", err)
	}
	assertContent(t, target, script("0.35.9"))
	// A second update waits for the restart of the pending one.
	if _, err := Update(context.Background(), "npc", "", UpdateOptions{}); !errors.Is(err, selfupdate.ErrPendingRestart) {
		t.Fatalf("Update(while pending) error = %v, want ErrPendingRestart", err)
	}
	j := selfupdate.JournalFor(target)
	var out selfupdate.Outcome
	for range updateBoots + 1 {
		var err error
		if out, err = j.Startup(context.Background(), updateBoots); err != nil {
			t.Fatalf("Startup() error = %v", err)
		}
		if out.Status != selfupdate.StatusPending {
			break
		}
	}
	if out.Status != selfupdate.StatusRolledBack {
		t.Fatalf("Startup() status = %v, want rolled back", out.Status)
	}
	assertContent(t, target, old)
}

func TestReleaseUpdaterSettings(t *testing.T) {
	u, err := releaseUpdater("npc", false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Product != "nps" || u.Name != artifactName("npc") || len(u.Trust.Keys) != 1 || len(u.Sources) != 1+len(cdnRepos) {
		t.Fatalf("updater = %+v", u)
	}
	if strings.Join(u.Grants, ",") != "user,rollout" {
		t.Fatalf("grants = %v", u.Grants)
	}
	u, err = releaseUpdater("nps", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(u.Grants, ",") != "user,rollout,force,reinstall,"+selfupdate.GrantDowngradeAny+",retry-failed" {
		t.Fatalf("force grants = %v", u.Grants)
	}
}
