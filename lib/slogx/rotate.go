package slogx

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rotation is a log file and when to rotate it. Zero MaxSize means 64 MiB;
// negative means never rotate by size. Every, when positive, also rotates a
// file that has been written for that long. Zero MaxFiles and MaxAge keep
// every rotated file.
type Rotation struct {
	Path     string
	MaxSize  int64 // bytes
	Every    time.Duration
	MaxFiles int
	MaxAge   time.Duration
	Compress bool
	// Mode is the permission of new log files; zero is 0640, readable by
	// the owner's group (such as an admin group running fail2ban) and no
	// one else. Directories made for them get the same bits plus search.
	Mode os.FileMode
}

func (c Rotation) mode() os.FileMode {
	if c.Mode == 0 {
		return 0o640
	}
	return c.Mode & os.ModePerm
}

func dirMode(m os.FileMode) os.FileMode { return m | (m&0o444)>>2 }

const defaultMaxSize = 64 << 20

// rotatedLayout is the time in a rotated file's name, UTC:
// <name>-20261108T031245.000[-N]<ext>[.gz].
const rotatedLayout = "20060102T150405.000"

// Rotating is an Output to a file that rotates itself: the full file is
// renamed to <name>-<UTC time><ext> and a new one started. Compression and
// pruning of rotated files run in order on one goroutine of its own, so they
// never race each other or the writer.
type Rotating struct {
	mu      sync.Mutex
	cfg     Rotation
	dir     string
	base    string // file name without ext
	ext     string
	f       *os.File
	size    int64
	opened  time.Time
	closed  bool
	work    chan string // rotated files to compress and prune
	workers sync.WaitGroup
}

// OpenRotating opens the file of c, creating its directory.
func OpenRotating(c Rotation) (*Rotating, error) {
	if c.MaxSize == 0 {
		c.MaxSize = defaultMaxSize
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), dirMode(c.mode())); err != nil {
		return nil, err
	}
	name := filepath.Base(c.Path)
	ext := filepath.Ext(name)
	r := &Rotating{cfg: c, dir: filepath.Dir(c.Path), base: strings.TrimSuffix(name, ext), ext: ext, work: make(chan string, 16)}
	if err := r.open(); err != nil {
		return nil, err
	}
	r.workers.Go(r.maintain)
	return r, nil
}

func (r *Rotating) open() error {
	f, err := os.OpenFile(r.cfg.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, r.cfg.mode())
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size, r.opened = f, fi.Size(), time.Now()
	return nil
}

func (r *Rotating) Write(p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return os.ErrClosed
	}
	if r.f == nil {
		if err := r.open(); err != nil {
			return err
		}
	}
	bySize := r.cfg.MaxSize > 0 && r.size > 0 && r.size+int64(len(p)) > r.cfg.MaxSize
	byTime := r.cfg.Every > 0 && r.size > 0 && time.Since(r.opened) >= r.cfg.Every
	if bySize || byTime {
		r.rotate()
		if r.f == nil {
			if err := r.open(); err != nil {
				return err
			}
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return err
}

// rotate renames the current file to a name no other rotated file has and
// starts a new one; r.mu is held.
func (r *Rotating) rotate() {
	_ = r.f.Close()
	r.f = nil
	stamp := time.Now().UTC().Format(rotatedLayout)
	var name string
	for i := 0; ; i++ {
		suffix := ""
		if i > 0 {
			suffix = "-" + strconv.Itoa(i)
		}
		name = filepath.Join(r.dir, r.base+"-"+stamp+suffix+r.ext)
		if !exists(name) && !exists(name+".gz") {
			break
		}
	}
	if os.Rename(r.cfg.Path, name) == nil {
		select {
		case r.work <- name:
		default: // maintenance is behind; the next rotation prunes this one too
		}
	}
	_ = r.open()
}

func exists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

// maintain compresses and prunes rotated files, in order.
func (r *Rotating) maintain() {
	for name := range r.work {
		if r.cfg.Compress {
			compressFile(name, r.cfg.mode())
		}
		r.prune()
	}
}

// Reopen closes and reopens the file, after an external rotation.
func (r *Rotating) Reopen() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return os.ErrClosed
	}
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

// Close closes the file and waits for compression and pruning; writes
// after it fail.
func (r *Rotating) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	var err error
	if r.f != nil {
		err = r.f.Close()
		r.f = nil
	}
	close(r.work)
	r.mu.Unlock()
	r.workers.Wait()
	return err
}

// rotated is a file that Rotating made, with the time in its name.
type rotated struct {
	path string
	at   time.Time
	seq  int
}

// rotatedFiles lists the files of the directory whose names are exactly
// what rotate produces for this file, newest first. Other files, such as a
// sibling log whose name starts the same, are never listed.
func (r *Rotating) rotatedFiles() []rotated {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	var out []rotated
	prefix := r.base + "-"
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) {
			continue
		}
		rest := strings.TrimSuffix(n[len(prefix):], ".gz")
		if !strings.HasSuffix(rest, r.ext) {
			continue
		}
		rest = rest[:len(rest)-len(r.ext)]
		if len(rest) < len(rotatedLayout) {
			continue
		}
		at, err := time.Parse(rotatedLayout, rest[:len(rotatedLayout)])
		if err != nil {
			continue
		}
		seq := 0
		if tail := rest[len(rotatedLayout):]; tail != "" {
			if tail[0] != '-' {
				continue
			}
			if seq, err = strconv.Atoi(tail[1:]); err != nil || seq < 1 {
				continue
			}
		}
		out = append(out, rotated{path: filepath.Join(r.dir, n), at: at, seq: seq})
	}
	slices.SortFunc(out, func(a, b rotated) int {
		if c := b.at.Compare(a.at); c != 0 {
			return c
		}
		return b.seq - a.seq
	})
	return out
}

// prune removes rotated files beyond MaxFiles or older than MaxAge. A file
// and its compressed copy (while compression runs) count once.
func (r *Rotating) prune() {
	if r.cfg.MaxFiles <= 0 && r.cfg.MaxAge <= 0 {
		return
	}
	kept := 0
	seen := map[string]bool{}
	for _, f := range r.rotatedFiles() {
		key := strings.TrimSuffix(f.path, ".gz")
		if seen[key] {
			continue
		}
		seen[key] = true
		drop := r.cfg.MaxFiles > 0 && kept >= r.cfg.MaxFiles
		if !drop && r.cfg.MaxAge > 0 && time.Since(f.at) > r.cfg.MaxAge {
			drop = true
		}
		if drop {
			_ = os.Remove(key)
			_ = os.Remove(key + ".gz")
			continue
		}
		kept++
	}
}

// compressFile writes name.gz through a temporary file, then removes name.
func compressFile(name string, mode os.FileMode) {
	in, err := os.Open(name)
	if err != nil {
		return
	}
	defer func() { _ = in.Close() }()
	tmp := name + ".gz.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
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
	if err == nil {
		err = os.Rename(tmp, name+".gz")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = in.Close()
	_ = os.Remove(name)
}
