package obs

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rotation is a log file and when to rotate it. Zero MaxSize means 64 MiB;
// negative means never rotate by size. Zero MaxFiles and MaxAge keep every
// rotated file.
type Rotation struct {
	Path     string
	MaxSize  int64 // bytes
	MaxFiles int
	MaxAge   time.Duration
	Compress bool
}

const defaultMaxSize = 64 << 20

// Rotating is an Output to a file that rotates itself by size: the full file is renamed
// to <name>-<time><ext> and a new one is started.
type Rotating struct {
	mu   sync.Mutex
	cfg  Rotation
	f    *os.File
	size int64
	bg   sync.WaitGroup
}

// OpenRotating opens the file of c, creating its directory.
func OpenRotating(c Rotation) (*Rotating, error) {
	if c.MaxSize == 0 {
		c.MaxSize = defaultMaxSize
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return nil, err
	}
	r := &Rotating{cfg: c}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rotating) open() error {
	f, err := os.OpenFile(r.cfg.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

func (r *Rotating) Write(p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return err
		}
	}
	if r.cfg.MaxSize > 0 && r.size > 0 && r.size+int64(len(p)) > r.cfg.MaxSize {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return err
}

// rotate renames the current file and starts a new one; r.mu is held.
func (r *Rotating) rotate() {
	_ = r.f.Close()
	r.f = nil
	ext := filepath.Ext(r.cfg.Path)
	base := strings.TrimSuffix(r.cfg.Path, ext)
	name := base + "-" + time.Now().Format("20060102T150405.000") + ext
	if os.Rename(r.cfg.Path, name) == nil {
		r.bg.Add(1)
		go func() {
			defer r.bg.Done()
			if r.cfg.Compress {
				compressFile(name)
			}
			r.prune(base, ext)
		}()
	}
	if err := r.open(); err != nil {
		r.f = nil
	}
}

// Reopen closes and reopens the file, after an external rotation.
func (r *Rotating) Reopen() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		_ = r.f.Close()
	}
	r.f = nil
	return r.open()
}

// Flush syncs the file to disk.
func (r *Rotating) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		return r.f.Sync()
	}
	return nil
}

func (r *Rotating) Close() error {
	r.mu.Lock()
	var err error
	if r.f != nil {
		err = r.f.Close()
		r.f = nil
	}
	r.mu.Unlock()
	r.bg.Wait()
	return err
}

// prune removes rotated files beyond MaxFiles or older than MaxAge.
func (r *Rotating) prune(base, ext string) {
	if r.cfg.MaxFiles <= 0 && r.cfg.MaxAge <= 0 {
		return
	}
	matches, _ := filepath.Glob(base + "-*" + ext + "*")
	var old []string
	for _, m := range matches {
		if strings.HasSuffix(m, ext) || strings.HasSuffix(m, ext+".gz") {
			old = append(old, m)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(old))) // newest first
	for i, m := range old {
		drop := r.cfg.MaxFiles > 0 && i >= r.cfg.MaxFiles
		if !drop && r.cfg.MaxAge > 0 {
			if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > r.cfg.MaxAge {
				drop = true
			}
		}
		if drop {
			_ = os.Remove(m)
		}
	}
}

func compressFile(name string) {
	in, err := os.Open(name)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.OpenFile(name+".gz", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(name + ".gz")
		return
	}
	_ = os.Remove(name)
}
