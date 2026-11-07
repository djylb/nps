package obs

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Config says where and how lines are written. The zero Config writes text
// at info level to stderr.
type Config struct {
	// Output is "stderr" (or "stdout"), "file", "both" (console and file)
	// or "off".
	Output string
	Format Format
	// Level is a default level and per-module overrides,
	// "info,bridge=debug,p2p=warn"; empty is info.
	Level string
	// File is the log file for "file" and "both".
	File Rotation
	// SecurityFile, when set, also receives every line of a security event
	// (Def.Security), whatever the module levels.
	SecurityFile Rotation
	// Buffer, when positive, keeps the last Buffer bytes of lines in memory
	// for TakeBuffered.
	Buffer int
}

// Sink receives every event defined with Def.Bus, after level filtering
// would have applied to its line but whether or not the line is written.
// It must not block.
type Sink func(d *Def, attrs []slog.Attr)

// Logs is a process's logging. Its loggers, from Module, keep working across
// Configure.
type Logs struct {
	st      atomic.Pointer[state]
	sink    atomic.Pointer[Sink]
	limiter limiter
	mu      sync.Mutex // Configure and Close
}

type state struct {
	format   Format
	console  *output
	file     *output
	security *output
	buffer   *ring
	levels   levels
	closers  []io.Closer
	fallback string // why the file could not be opened
}

// New returns Logs that write text at info level to stderr until Configure.
func New() *Logs {
	l := &Logs{}
	l.st.Store(&state{console: &output{w: os.Stderr}, levels: levels{def: slog.LevelInfo}})
	return l
}

// Default is the Logs that Module uses.
var Default = New()

// Module returns a logger of Default whose lines carry module=name.
func Module(name string) *slog.Logger { return Default.Module(name) }

// Module returns a logger whose lines carry module=name.
func (l *Logs) Module(name string) *slog.Logger {
	return slog.New(&handler{logs: l, module: name})
}

// Configure replaces how l writes. A file that cannot be opened falls back
// to stderr with a warning line rather than failing; an invalid Level is an
// error and changes nothing.
func (l *Logs) Configure(c Config) error {
	lv, err := parseLevels(c.Level)
	if err != nil {
		return err
	}
	st := &state{format: c.Format, levels: lv}
	out := strings.ToLower(c.Output)
	switch out {
	case "", "stderr", "stdout", "console", "file", "both", "off":
	default:
		return fmt.Errorf("unknown log output %q", c.Output)
	}
	wantFile := out == "file" || out == "both"
	if wantFile && c.File.Path != "" {
		w, err := openRotating(c.File)
		if err != nil {
			st.fallback = err.Error()
			wantFile = false
		} else {
			st.file = &output{w: w}
			st.closers = append(st.closers, w)
		}
	} else if wantFile {
		wantFile = false
	}
	switch {
	case out == "stdout":
		st.console = &output{w: os.Stdout}
	case out == "off":
	case out == "file" && wantFile:
	default: // stderr, console, both, or a file that could not be opened
		st.console = &output{w: os.Stderr}
	}
	if c.SecurityFile.Path != "" {
		if w, err := openRotating(c.SecurityFile); err == nil {
			st.security = &output{w: w}
			st.closers = append(st.closers, w)
		} else if st.fallback == "" {
			st.fallback = err.Error()
		}
	}
	if c.Buffer > 0 {
		st.buffer = newRing(c.Buffer)
	}
	l.mu.Lock()
	old := l.st.Swap(st)
	l.mu.Unlock()
	for _, c := range old.closers {
		_ = c.Close()
	}
	if st.fallback != "" {
		Emit(l.Module("obs"), &LogFallback, "reason", st.fallback)
	}
	return nil
}

// SetLevel changes the levels as Config.Level says.
func (l *Logs) SetLevel(spec string) error {
	lv, err := parseLevels(spec)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cur := l.st.Load()
	n := *cur
	n.levels = lv
	l.st.Store(&n)
	return nil
}

// SetSink sets the Sink of bus events; nil removes it.
func (l *Logs) SetSink(s Sink) {
	if s == nil {
		l.sink.Store(nil)
		return
	}
	l.sink.Store(&s)
}

// Reopen reopens the log files, for an external logrotate (SIGHUP).
func (l *Logs) Reopen() {
	st := l.st.Load()
	for _, c := range st.closers {
		if r, ok := c.(*rotating); ok {
			r.reopen()
		}
	}
}

// TakeBuffered returns and clears the in-memory buffer.
func (l *Logs) TakeBuffered() string {
	if b := l.st.Load().buffer; b != nil {
		return b.take()
	}
	return ""
}

// Close closes the files; lines after it go to stderr.
func (l *Logs) Close() {
	l.mu.Lock()
	old := l.st.Swap(&state{console: &output{w: os.Stderr}, levels: levels{def: slog.LevelInfo}})
	l.mu.Unlock()
	for _, c := range old.closers {
		_ = c.Close()
	}
}

func (l *Logs) enabled(module string, lv slog.Level) bool {
	st := l.st.Load()
	return (st.console != nil || st.file != nil || st.buffer != nil) && lv >= st.levels.of(module)
}

func (l *Logs) emit(ln *line) {
	st := l.st.Load()
	d := lookup(ln.event)
	if d != nil && d.Limit != "" {
		n, ok := l.limiter.allow(d, ln.attrs, ln.t)
		if !ok {
			return
		}
		if n > 0 {
			ln.attrs = append(ln.attrs, slog.Uint64(KeySuppressed, n))
		}
	}
	if d != nil && d.Bus {
		if s := l.sink.Load(); s != nil {
			(*s)(d, ln.attrs)
		}
	}
	toMain := ln.level >= st.levels.of(ln.module)
	toSec := d != nil && d.Security && st.security != nil
	if !toMain && !toSec {
		return
	}
	bp := ln.encode(st.format)
	if toMain {
		if st.console != nil {
			st.console.write(*bp)
		}
		if st.file != nil {
			st.file.write(*bp)
		}
		if st.buffer != nil {
			st.buffer.write(*bp)
		}
	}
	if toSec {
		st.security.write(*bp)
	}
	if cap(*bp) <= 64<<10 {
		bufPool.Put(bp)
	}
}

// levels is a default level and per-module ones.
type levels struct {
	def     slog.Level
	modules map[string]slog.Level
}

func (v levels) of(module string) slog.Level {
	if l, ok := v.modules[module]; ok {
		return l
	}
	return v.def
}

// ParseLevel reads one level name: trace, debug, info, warn, error, off.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "7":
		return LevelTrace, nil
	case "debug", "6":
		return slog.LevelDebug, nil
	case "", "info", "notice", "5":
		return slog.LevelInfo, nil
	case "warn", "warning", "4":
		return slog.LevelWarn, nil
	case "error", "fatal", "panic", "critical", "alert", "emergency", "3", "2", "1":
		return slog.LevelError, nil
	case "off", "disabled", "none", "0":
		return slog.Level(1 << 20), nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

func parseLevels(spec string) (levels, error) {
	v := levels{def: slog.LevelInfo}
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		mod, name, ok := strings.Cut(part, "=")
		if !ok {
			l, err := ParseLevel(part)
			if err != nil {
				return levels{}, err
			}
			v.def = l
			continue
		}
		l, err := ParseLevel(name)
		if err != nil {
			return levels{}, err
		}
		mod = strings.TrimSpace(mod)
		if mod == "" {
			return levels{}, errors.New("log level without a module: " + part)
		}
		if v.modules == nil {
			v.modules = map[string]slog.Level{}
		}
		v.modules[mod] = l
	}
	return v, nil
}

// ring keeps the last bytes written.
type ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRing(n int) *ring { return &ring{max: n} }

func (r *ring) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.max; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
	}
}

func (r *ring) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(r.buf)
	r.buf = r.buf[:0]
	return s
}
