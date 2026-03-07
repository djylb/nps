package install

import (
	"os"
	"path/filepath"
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

func TestCopyFileSamePathNoop(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "same.txt")
	if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	n, err := copyFile(p, p)
	if err != nil {
		t.Fatalf("copyFile(same path) error = %v", err)
	}
	if n != 0 {
		t.Fatalf("copyFile(same path) copied = %d, want 0", n)
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
