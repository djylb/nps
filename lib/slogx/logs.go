package slogx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Keys of the fixed fields.
const (
	KeyEvent      = "event"
	KeyModule     = "module"
	KeySuppressed = "suppressed"
)

// Config says where and how lines go. The zero Config writes logfmt at info
// level to stderr.
//
// The fields up to Routes are shorthands for the common routes: a console,
// a file, a security file and an in-memory viewer, all but the security
// file taking the lines the level table lets through. Routes adds any
// others.
type Config struct {
	// Output is "stderr", "stdout", "file", "both" (stderr and file) or
	// "off".
	Output string
	// Format of the console and the files.
	Format Format
	// Pretty makes the console use PrettyEncoder when it is a terminal,
	// whatever Format says, coloured.
	Pretty bool
	// Level is a default level and per-module ones, "info,db=debug".
	Level string
	// File is the log file for "file" and "both".
	File Rotation
	// SecurityFile, when set, receives every security event.
	SecurityFile Rotation
	// Memory, when positive, keeps that many entries for Logs.Memory.
	Memory int
	// Async, when positive, writes files through a queue of that many
	// lines, so a slow disk never slows the caller; lines that do not fit
	// are dropped and counted.
	Async int
	// Routes are further destinations.
	Routes []Route

	// AddSource adds source=file:line to every line.
	AddSource bool
	// StackFrom, when set, adds the stack to lines at or above it.
	StackFrom *slog.Level
	// Context returns attributes a context carries, such as a request id,
	// for lines logged with one.
	Context func(context.Context) []slog.Attr
	// Sample, when set, samples plain lines (not events) per module,
	// level and message.
	Sample *Sampling
}

// Sink receives every bus event (Def.Bus), whatever the levels. It must not
// block.
type Sink func(d *Def, attrs []slog.Attr)

// Logs is a process's logging. Its loggers keep working across Configure.
type Logs struct {
	levels  *Levels
	pipe    atomic.Pointer[pipeline]
	sink    atomic.Pointer[Sink]
	sampler sampler
	mu      sync.Mutex // Configure and Close
	taken   atomic.Uint64
}

// pipeline is one configuration, replaced whole.
type pipeline struct {
	routes    []Route
	below     bool // a route sees lines below the level table
	closers   []Output
	memory    *Memory
	addSource bool
	stackFrom *slog.Level
	ctxAttrs  func(context.Context) []slog.Attr
	sample    *Sampling
}

// New returns Logs that write logfmt at info level to stderr until
// Configure.
func New() *Logs {
	l := &Logs{levels: newLevels()}
	l.pipe.Store(&pipeline{routes: []Route{{Name: "console", Output: WriterOutput(os.Stderr)}}})
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

// Handler returns a handler with no module, for slog.SetDefault or a
// library that takes one.
func (l *Logs) Handler() slog.Handler { return &handler{logs: l} }

// Levels returns the level table.
func (l *Logs) Levels() *Levels { return l.levels }

// SetLevel is Levels().Set.
func (l *Logs) SetLevel(spec string) error { return l.levels.Set(spec) }

// Memory returns the in-memory entries of Config.Memory, or nil.
func (l *Logs) Memory() *Memory { return l.pipe.Load().memory }

// Configure replaces where and how l writes. A file that cannot be opened
// falls back to stderr with a log_fallback line rather than failing; an
// invalid Output or Level is an error and changes nothing.
func (l *Logs) Configure(c Config) error {
	lv, err := parseLevels(c.Level)
	if err != nil {
		return err
	}
	out := strings.ToLower(c.Output)
	switch out {
	case "", "stderr", "stdout", "console", "file", "both", "off":
	default:
		return fmt.Errorf("unknown log output %q", c.Output)
	}
	p := &pipeline{addSource: c.AddSource, stackFrom: c.StackFrom, ctxAttrs: c.Context, sample: c.Sample}
	var fallback []string
	open := func(r Rotation) Output {
		f, err := OpenRotating(r)
		if err != nil {
			fallback = append(fallback, err.Error())
			return nil
		}
		var o Output = f
		if c.Async > 0 {
			o = Async(f, c.Async)
		}
		p.closers = append(p.closers, o)
		return o
	}
	enc := c.Format.Encoder(false)
	console := out == "" || out == "stderr" || out == "stdout" || out == "console" || out == "both"
	if out == "file" || out == "both" {
		if c.File.Path == "" {
			console = true
		} else if o := open(c.File); o != nil {
			p.routes = append(p.routes, Route{Name: "file", Encoder: enc, Output: o})
		} else {
			console = true
		}
	}
	if console {
		w := os.Stderr
		if out == "stdout" {
			w = os.Stdout
		}
		ce := enc
		if c.Pretty && isTerminal(w) {
			ce = PrettyEncoder{Color: true}
		}
		p.routes = append(p.routes, Route{Name: "console", Encoder: ce, Output: WriterOutput(w)})
	}
	if c.SecurityFile.Path != "" {
		if o := open(c.SecurityFile); o != nil {
			p.routes = append(p.routes, Route{Name: "security", Match: SecurityEvents, Encoder: enc, Output: o})
		}
	}
	if c.Memory > 0 {
		p.memory = NewMemory(c.Memory)
		p.routes = append(p.routes, Route{Name: "memory", Memory: p.memory})
	}
	p.routes = append(p.routes, c.Routes...)
	for _, r := range p.routes {
		p.below = p.below || r.Below
	}
	l.mu.Lock()
	l.levels.mu.Lock()
	l.levels.base = lv
	l.levels.publish()
	l.levels.mu.Unlock()
	old := l.pipe.Swap(p)
	l.mu.Unlock()
	closeAll(old.closers)
	for _, f := range fallback {
		Emit(l.Module("slogx"), &LogFallback, "reason", f)
	}
	return nil
}

func closeAll(os []Output) {
	for _, o := range os {
		_ = o.Close()
	}
}

// SetSink sets the Sink of bus events; nil removes it.
func (l *Logs) SetSink(s Sink) {
	if s == nil {
		l.sink.Store(nil)
		return
	}
	l.sink.Store(&s)
}

// Reopen reopens the log files, after an external rotation (SIGHUP).
func (l *Logs) Reopen() {
	for _, o := range l.pipe.Load().closers {
		if r, ok := o.(Reopener); ok {
			_ = r.Reopen()
		}
	}
}

// Flush writes out what asynchronous outputs hold and syncs the files.
func (l *Logs) Flush() {
	for _, r := range l.pipe.Load().routes {
		if f, ok := r.Output.(Flusher); ok {
			_ = f.Flush()
		}
	}
}

// TakeBuffered returns the in-memory entries not taken before, as logfmt.
func (l *Logs) TakeBuffered() string {
	m := l.Memory()
	if m == nil {
		return ""
	}
	es := m.Query(Query{After: l.taken.Load(), AnyLevel: true})
	if len(es) == 0 {
		return ""
	}
	l.taken.Store(es[len(es)-1].Seq)
	var b []byte
	for _, e := range es {
		r := Record{Time: e.Time, Level: e.Level, Module: e.Module, Event: e.Event, Msg: e.Msg}
		for _, f := range e.Attrs {
			r.Attrs = append(r.Attrs, slog.String(f.Key, f.Value))
		}
		b = TextEncoder{}.Encode(b, &r)
	}
	return string(b)
}

// Close flushes and closes the files; lines after it go to stderr.
func (l *Logs) Close() {
	l.mu.Lock()
	old := l.pipe.Swap(&pipeline{routes: []Route{{Name: "console", Output: WriterOutput(os.Stderr)}}})
	l.mu.Unlock()
	closeAll(old.closers)
	l.levels.stop()
}

// enabled reports whether a line of module at lv may be written anywhere.
func (l *Logs) enabled(module string, lv slog.Level) bool {
	if l.pipe.Load().below {
		return true
	}
	return lv >= l.levels.Of(module)
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 0, 512); return &b }}

// dispatch samples r and hands it to the routes that take it.
func (l *Logs) dispatch(p *pipeline, r *Record) {
	if d := r.Def; d != nil {
		if s, field, ok := d.sampling(); ok {
			n, pass := l.sampler.allow(sampleKey(r, field), s, r.Time)
			if !pass {
				return
			}
			if n > 0 {
				r.Attrs = append(r.Attrs, slog.Uint64(KeySuppressed, n))
			}
		}
		if d.Bus {
			if s := l.sink.Load(); s != nil {
				(*s)(d, r.Attrs)
			}
		}
	} else if p.sample != nil {
		n, pass := l.sampler.allow(sampleKey(r, ""), *p.sample, r.Time)
		if !pass {
			return
		}
		if n > 0 {
			r.Attrs = append(r.Attrs, slog.Uint64(KeySuppressed, n))
		}
	}
	bp := bufPool.Get().(*[]byte)
	for i := range p.routes {
		rt := &p.routes[i]
		if rt.takes(r, l.levels) {
			rt.deliver(r, bp)
		}
	}
	if cap(*bp) <= 64<<10 {
		bufPool.Put(bp)
	}
}

func source(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	fs := runtime.CallersFrames([]uintptr{pc})
	f, _ := fs.Next()
	if f.File == "" {
		return ""
	}
	file := f.File
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		if j := strings.LastIndexByte(file[:i], '/'); j >= 0 {
			file = file[j+1:]
		}
	}
	return file + ":" + strconv.Itoa(f.Line)
}

func stack() string {
	buf := make([]byte, 16<<10)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
