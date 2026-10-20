package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/djylb/nps/lib/version"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathExists(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existing, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if ok, err := pathExists(existing); err != nil || !ok {
		t.Fatalf("pathExists(existing) = (%v, %v), want (true, nil)", ok, err)
	}

	missing := filepath.Join(tmpDir, "missing.txt")
	if ok, err := pathExists(missing); err != nil || ok {
		t.Fatalf("pathExists(missing) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	content := []byte("nps-copy-file")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("WriteFile(src) error = %v", err)
	}

	dest := filepath.Join(tmpDir, "nested", "dest.txt")
	n, err := copyFile(src, dest)
	if err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("copyFile() copied = %d, want %d", n, len(content))
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile(dest) error = %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("dest content = %q, want %q", got, content)
	}
}

func TestCopyFileSameFileNoop(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "same.txt")
	if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(tmpDir, "link.txt")
	if err := os.Link(p, link); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	for _, dest := range []string{p, link} {
		n, err := copyFile(p, dest)
		if err != nil {
			t.Fatalf("copyFile(%s) error = %v", dest, err)
		}
		if n != 0 {
			t.Fatalf("copyFile(%s) copied = %d, want 0", dest, n)
		}
	}
	if got, err := os.ReadFile(p); err != nil || string(got) != "same" {
		t.Fatalf("ReadFile() = (%q, %v), want same", got, err)
	}
}

func TestCopyDir(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(filepath.Join(src, "inner"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "inner", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile(a.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile(b.txt) error = %v", err)
	}

	dest := filepath.Join(tmpDir, "dest")
	if err := CopyDir(src, dest); err != nil {
		t.Fatalf("CopyDir() error = %v", err)
	}

	for rel, want := range map[string]string{
		"inner/a.txt": "A",
		"b.txt":       "B",
	} {
		got, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("dest %s = %q, want %q", rel, got, want)
		}
	}
}

func TestCopyDirValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	notDir := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	destDir := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(destDir) error = %v", err)
	}

	if err := CopyDir(notDir, destDir); err == nil {
		t.Fatal("CopyDir(non-directory src) error = nil, want error")
	}

	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error = %v", err)
	}
	if err := CopyDir(srcDir, notDir); err == nil {
		t.Fatal("CopyDir(file destination) error = nil, want error")
	}
}

func TestReplaceExecutables(t *testing.T) {
	for _, aside := range []bool{false, true} {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src")
		writeTestFile(t, src, "new")
		a := filepath.Join(tmpDir, "a")
		b := filepath.Join(tmpDir, "b")
		writeTestFile(t, a, "old")
		writeTestFile(t, b, "old")

		if err := replaceExecutables(src, []string{a, b}, aside); err != nil {
			t.Fatalf("replaceExecutables(aside=%v) error = %v", aside, err)
		}
		for _, p := range []string{a, b} {
			if got, err := os.ReadFile(p); err != nil || string(got) != "new" {
				t.Fatalf("aside=%v: ReadFile(%s) = (%q, %v), want new", aside, p, got, err)
			}
			if runtime.GOOS != "windows" {
				fi, err := os.Stat(p)
				if err != nil {
					t.Fatalf("aside=%v: Stat(%s) error = %v", aside, p, err)
				}
				if fi.Mode().Perm() != 0o755 {
					t.Fatalf("aside=%v: %s mode = %v, want 0755", aside, p, fi.Mode().Perm())
				}
			}
		}
		assertOnlyFiles(t, tmpDir, "a", "b", "src")
	}
}

func TestCommitExecutablesRollback(t *testing.T) {
	for _, aside := range []bool{false, true} {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src")
		writeTestFile(t, src, "new")
		a := filepath.Join(tmpDir, "a")
		b := filepath.Join(tmpDir, "b")
		writeTestFile(t, a, "old-a")
		writeTestFile(t, b, "old-b")

		staged, err := stageExecutables(src, []string{a, b})
		if err != nil {
			t.Fatalf("stageExecutables() error = %v", err)
		}
		if err := os.Remove(b + ".new"); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if err := commitExecutables(staged, aside); err == nil {
			t.Fatalf("commitExecutables(aside=%v) error = nil, want error", aside)
		}
		for p, want := range map[string]string{a: "old-a", b: "old-b"} {
			if got, err := os.ReadFile(p); err != nil || string(got) != want {
				t.Fatalf("aside=%v: ReadFile(%s) = (%q, %v), want %q", aside, p, got, err, want)
			}
		}
		assertOnlyFiles(t, tmpDir, "a", "b", "src")
	}
}

func TestParseServiceExecutable(t *testing.T) {
	if got := parseSystemdExecStart("{ path=/usr/bin/nps ; argv[]=/usr/bin/nps service ; ignore_errors=no }\n"); got != "/usr/bin/nps" {
		t.Fatalf("parseSystemdExecStart() = %q", got)
	}
	if got := parseSysvCmd("#!/bin/sh\ncmd=\"/usr/bin/npc -server=a:1 -debug=false\"\nname=x\n"); got != "/usr/bin/npc" {
		t.Fatalf("parseSysvCmd() = %q", got)
	}
	for out, want := range map[string]string{
		"        BINARY_PATH_NAME   : \"C:\\Program Files\\nps\\nps.exe\" service\r\n": `C:\Program Files\nps\nps.exe`,
		"        BINARY_PATH_NAME   : C:\\nps\\npc.exe -debug=false\r\n":               `C:\nps\npc.exe`,
	} {
		if got := parseScBinaryPath("[SC] QueryServiceConfig SUCCESS\r\n" + out); got != want {
			t.Fatalf("parseScBinaryPath() = %q, want %q", got, want)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func TestRemoveAsideFiles(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"nps.exe", "nps.exe.old", "nps.exe.k3x9.old", "npc.exe.old", "nps.exe.new"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	removeAsideFiles(filepath.Join(tmpDir, "nps.exe"))
	assertOnlyFiles(t, tmpDir, "npc.exe.old", "nps.exe", "nps.exe.new")
}

func TestCopyServerAssetsPerItem(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src")
	dest := filepath.Join(tmpDir, "dest")
	for path, content := range map[string]string{
		"src/web/static/app.js": "js",
		"src/conf/nps.conf":     "conf",
		"dest/web/views/a.html": "kept",
		"dest/conf/nps.conf":    "user",
	} {
		p := filepath.Join(tmpDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	if err := copyServerAssets(src, dest); err != nil {
		t.Fatalf("copyServerAssets() error = %v", err)
	}
	for path, want := range map[string]string{
		"web/views/a.html":      "kept",
		"web/static/app.js":     "js",
		"conf/nps.conf.default": "conf",
		"conf/nps.conf":         "user",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Fatalf("ReadFile(%s) = (%q, %v), want %q", path, got, err, want)
		}
	}

	if err := copyServerAssets(src, filepath.Join(tmpDir, "empty")); err == nil {
		t.Fatal("copyServerAssets(missing views, no existing copy) error = nil, want error")
	}
}

func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestReleaseSHA256(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	rl := &release{TagName: "v1.0.0"}
	for _, a := range []struct{ name, digest string }{
		{"ok.tar.gz", "sha256:" + strings.ToUpper(sum)},
		{"none.tar.gz", ""},
		{"short.tar.gz", "sha256:abcd"},
	} {
		rl.Assets = append(rl.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Digest             string `json:"digest"`
		}{Name: a.name, Digest: a.digest})
	}

	if got, err := rl.sha256("ok.tar.gz"); err != nil || got != sum {
		t.Fatalf("sha256(ok) = (%q, %v), want %q", got, err, sum)
	}
	if got, err := rl.sha256("none.tar.gz"); err != nil || got != "" {
		t.Fatalf("sha256(none) = (%q, %v), want (\"\", nil)", got, err)
	}
	for _, name := range []string{"short.tar.gz", "missing.tar.gz"} {
		if _, err := rl.sha256(name); err == nil {
			t.Fatalf("sha256(%s) error = nil, want error", name)
		}
	}
}

func TestHTTPClientCertificateFallback(t *testing.T) {
	payload := []byte("package")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	c := newHTTPClient()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, verified, err := c.do(req)
	if err != nil {
		t.Fatalf("do(self-signed) error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if verified || string(body) != string(payload) {
		t.Fatalf("do(self-signed) = (%q, verified=%v), want (%q, false)", body, verified, payload)
	}

	dest := filepath.Join(t.TempDir(), "pkg.tar.gz")
	if err := downloadArchive(c, srv.URL, dest, ""); err == nil {
		t.Fatal("downloadArchive(unverified, no digest) error = nil, want error")
	}
	sum := sha256.Sum256(payload)
	if err := downloadArchive(c, srv.URL, dest, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("downloadArchive(unverified, digest) error = %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	closedURL := "https://" + ln.Addr().String()
	_ = ln.Close()
	req, _ = http.NewRequest(http.MethodGet, closedURL, nil)
	if _, _, err := c.do(req); err == nil || isCertificateError(err) {
		t.Fatalf("do(closed port) error = %v, want a non-certificate error", err)
	}
}

// Even with force, release metadata and package validation must remain mandatory.
func TestForceUpdateBypassesVersionGateButValidatesDigest(t *testing.T) {
	for _, kind := range []string{"client", "server"} {
		for _, tag := range []string{"v" + version.VERSION, "v0.1.0"} {
			for _, force := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/force=%v", kind, tag, force), func(t *testing.T) {
					requests := 0
					transport := updateTestTransport(func(req *http.Request) (*http.Response, error) {
						requests++
						if req.URL.Host != "api.github.com" {
							t.Fatalf("unexpected download: %s", req.URL)
						}
						body := fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":%q,"digest":"sha256:invalid"}]}`, tag, packageName(kind))
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
					})
					c := &httpClient{verified: &http.Client{Transport: transport}}
					res, err := updateWithClient(c, kind, "unused", "unused", force)
					if res != nil {
						t.Fatal("unexpected replacement")
					}
					if force {
						if err == nil || !strings.Contains(err.Error(), "malformed digest") {
							t.Fatalf("forced update must validate digest: %v", err)
						}
					} else if err != nil {
						t.Fatalf("normal update should skip current/older release: %v", err)
					}
					if requests != 1 {
						t.Fatalf("requests = %d, want metadata only", requests)
					}
				})
			}
		}
	}
}

type updateTestTransport func(*http.Request) (*http.Response, error)

func (f updateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
