// Package instancekey loads persistent instance secrets and derives purpose-specific keys.
package instancekey

import (
	"bytes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const header = "nps-instance-key-v1\n"
const keySize = 32

// Key holds a 32-byte instance secret. Use DeriveKey rather than sharing the secret.
type Key struct{ secret [keySize]byte }

// LoadOrCreate reads an existing key or atomically installs a fully written new key.
// The file contains a version, a 32-byte secret and a checksum to detect corruption.
// Existing files are never replaced, including malformed or unreadable files.
func LoadOrCreate(path string) (*Key, error) {
	key, err := read(path)
	if !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	// Write in the destination directory so linking the completed file is atomic.
	// A hard link publishes without overwriting a concurrent creator's key.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".instance-key-*")
	if err != nil {
		return nil, fmt.Errorf("create instance key: %w", err)
	}
	name := tmp.Name()
	// Cleanup is best-effort; the successful path checks Close before publishing.
	defer func() { _ = os.Remove(name) }()
	defer func() { _ = tmp.Close() }()
	secret := make([]byte, keySize)
	if _, err = rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate instance key: %w", err)
	}
	data := append([]byte(header), secret...)
	checksum := sha256.Sum256(data)
	data = append(data, checksum[:]...)
	if _, err = tmp.Write(data); err != nil {
		return nil, fmt.Errorf("write instance key: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return nil, fmt.Errorf("sync instance key: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return nil, fmt.Errorf("close instance key: %w", err)
	}
	if err = os.Link(name, path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("publish instance key %s: %w", path, err)
	}
	return read(path)
}

func read(path string) (*Key, error) {
	// Lstat also ensures a dangling symlink is not treated as a missing key.
	if _, err := os.Lstat(path); err != nil {
		return nil, fmt.Errorf("stat instance key %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect instance key %s: %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(header)+2*keySize) {
		return nil, fmt.Errorf("invalid instance key file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read instance key %s: %v", path, err)
	}
	if len(data) != len(header)+2*keySize || !bytes.HasPrefix(data, []byte(header)) {
		return nil, fmt.Errorf("invalid instance key format: %s", path)
	}
	sum := sha256.Sum256(data[:len(header)+keySize])
	if !bytes.Equal(sum[:], data[len(header)+keySize:]) {
		return nil, fmt.Errorf("invalid instance key checksum: %s", path)
	}
	key := &Key{}
	copy(key.secret[:], data[len(header):len(header)+keySize])
	return key, nil
}

// DeriveKey uses HKDF-SHA256 with an independent purpose for each use.
func (k *Key) DeriveKey(purpose string, length int) ([]byte, error) {
	if k == nil || purpose == "" || length <= 0 {
		return nil, fmt.Errorf("invalid instance key derivation parameters")
	}
	return hkdf.Key(sha256.New, k.secret[:], nil, purpose, length)
}
