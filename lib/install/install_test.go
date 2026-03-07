package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathExists(t *testing.T) {
	tmp := t.TempDir()

	exists, err := pathExists(tmp)
	if err != nil {
		t.Fatalf("pathExists(existing dir) error = %v", err)
	}
	if !exists {
		t.Fatal("pathExists(existing dir) = false, want true")
	}

	missing := filepath.Join(tmp, "missing")
	exists, err = pathExists(missing)
	if err != nil {
		t.Fatalf("pathExists(missing path) error = %v", err)
	}
	if exists {
		t.Fatal("pathExists(missing path) = true, want false")
	}
}

func TestCopyFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source.txt")
	content := []byte("hello-nps")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("WriteFile(src) error = %v", err)
	}

	dest := filepath.Join(tmp, "nested", "dest.txt")
	n, err := copyFile(src, dest)
	if err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("copyFile() bytes = %d, want %d", n, len(content))
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
	tmp := t.TempDir()
	file := filepath.Join(tmp, "same.txt")
	if err := os.WriteFile(file, []byte("same"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	n, err := copyFile(file, file)
	if err != nil {
		t.Fatalf("copyFile(same,same) error = %v", err)
	}
	if n != 0 {
		t.Fatalf("copyFile(same,same) bytes = %d, want 0", n)
	}
}

func TestCopyDir(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dest := filepath.Join(tmp, "dest")

	if err := os.MkdirAll(filepath.Join(src, "child"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatalf("WriteFile(root.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "child", "leaf.txt"), []byte("leaf"), 0o644); err != nil {
		t.Fatalf("WriteFile(leaf.txt) error = %v", err)
	}

	if err := CopyDir(src, dest); err != nil {
		t.Fatalf("CopyDir() error = %v", err)
	}

	for rel, want := range map[string]string{
		"root.txt":       "root",
		"child/leaf.txt": "leaf",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("content %s = %q, want %q", rel, got, want)
		}
	}
}

func TestCopyDirValidationErrors(t *testing.T) {
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "src-file")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(srcFile) error = %v", err)
	}
	if err := CopyDir(srcFile, filepath.Join(tmp, "dest")); err == nil {
		t.Fatal("CopyDir(src file, dir) error = nil, want non-nil")
	}

	srcDir := filepath.Join(tmp, "src-dir")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error = %v", err)
	}
	destFile := filepath.Join(tmp, "dest-file")
	if err := os.WriteFile(destFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(destFile) error = %v", err)
	}
	if err := CopyDir(srcDir, destFile); err == nil {
		t.Fatal("CopyDir(src dir, dest file) error = nil, want non-nil")
	}
}

func TestMkidrDirAll(t *testing.T) {
	tmp := t.TempDir()
	MkidrDirAll(tmp, "a", filepath.Join("b", "c"))

	for _, rel := range []string{"a", filepath.Join("b", "c")} {
		p := filepath.Join(tmp, rel)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", p, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", p)
		}
	}
}
