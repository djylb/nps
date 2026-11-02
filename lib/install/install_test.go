package install

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			Name   string `json:"name"`
			Digest string `json:"digest"`
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

	dest := filepath.Join(t.TempDir(), "geoip.dat")
	if err := downloadChecked(c, srv.URL, dest, strings.Repeat("00", 32)); err == nil {
		t.Fatal("downloadChecked(wrong digest) error = nil, want error")
	}
	sum := sha256.Sum256(payload)
	if err := downloadChecked(c, srv.URL, dest, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("downloadChecked(unverified, digest) error = %v", err)
	}
	// Updates go through RoundTrip, which falls back the same way.
	resp, err = (&http.Client{Transport: c}).Get(srv.URL)
	if err != nil {
		t.Fatalf("RoundTrip(self-signed) error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != string(payload) {
		t.Fatalf("RoundTrip(self-signed) = %q, want %q", body, payload)
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

func TestInstallFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	writeTestFile(t, src, "new")
	dest := filepath.Join(dir, "npc")
	writeTestFile(t, dest, "old")
	if err := installFile(src, dest); err != nil {
		t.Fatalf("installFile() error = %v", err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "new" {
		t.Fatalf("ReadFile() = (%q, %v), want new", got, err)
	}
	if err := installFile(dest, dest); err != nil {
		t.Fatalf("installFile(onto itself) error = %v", err)
	}
	assertOnlyFiles(t, dir, "npc", "src")
}

func TestArtifactName(t *testing.T) {
	orig := BuildTarget
	t.Cleanup(func() { BuildTarget = orig })
	BuildTarget = ""
	if got := artifactName("npc"); got != "npc" {
		t.Fatalf("artifactName() = %q", got)
	}
	BuildTarget = "v7"
	if got := artifactName("nps"); got != "nps-v7" {
		t.Fatalf("artifactName(v7) = %q", got)
	}
}
