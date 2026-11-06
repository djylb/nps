package instancekey

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestConcurrentCreationAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.key")
	const n = 32
	results := make(chan []byte, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			key, err := LoadOrCreate(path)
			if err != nil {
				t.Error(err)
				return
			}
			derived, err := key.DeriveKey("test", 32)
			if err != nil {
				t.Error(err)
				return
			}
			results <- derived
		})
	}
	wg.Wait()
	close(results)
	var first []byte
	count := 0
	for value := range results {
		count++
		if first == nil {
			first = value
		}
		if !bytes.Equal(first, value) {
			t.Fatal("concurrent creators used different keys")
		}
	}
	if count != n {
		t.Fatalf("successful creators: %d", count)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := key.DeriveKey("test", 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Fatal("key changed after reload")
	}
	other, err := key.DeriveKey("other purpose", 32)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, other) {
		t.Fatal("purposes share keys")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing file was overwritten")
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".instance-key-*"))
	if len(files) != 0 {
		t.Fatalf("temporary files left: %v", files)
	}
}

func TestInvalidExistingKeyNeverReplaced(t *testing.T) {
	for _, name := range []string{"empty", "truncated", "corrupt", "directory"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "instance.key")
			if name == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := LoadOrCreate(path); err == nil {
					t.Fatal("accepted directory")
				}
				return
			}
			if _, err := LoadOrCreate(path); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			switch name {
			case "empty":
				data = nil
			case "truncated":
				data = data[:32]
			case "corrupt":
				data[len(header)] ^= 1
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreate(path); err == nil {
				t.Fatal("accepted damaged key")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(data, after) {
				t.Fatal("damaged key was replaced")
			}
		})
	}
}
