package slogx

import (
	"context"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"time"
)

// Def defines an event: its name, level and where else it goes. An
// application declares its events as package variables, so that the main
// log, the security log and an event bus all take an event from one
// definition. The record carries its Def, so two libraries may define
// events without knowing of each other.
type Def struct {
	Name  string
	Level slog.Level
	// Security events match SecurityEvents (a security log).
	Security bool
	// Bus events match BusEvents (an event bus, a webhook).
	Bus bool
	// Limit, when set, is the field whose value keys rate limiting: one
	// line per event and value per LimitWindow, the next carrying how many
	// were suppressed. "*" limits the event as a whole. Sample, when set,
	// is used instead.
	Limit  string
	Sample *Sampling
}

func (d *Def) sampling() (Sampling, string, bool) {
	if d.Sample != nil {
		return *d.Sample, d.Limit, true
	}
	if d.Limit != "" {
		return Sampling{First: 1, Tick: LimitWindow}, d.Limit, true
	}
	return Sampling{}, "", false
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*Def{}
)

// Register lists defs for Registered, such as for generating documentation
// of the events an application writes. Logging does not need it.
// Registering a name twice with different definitions panics.
func Register(defs ...*Def) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, d := range defs {
		if old, ok := registry[d.Name]; ok && old != d && (old.Security != d.Security || old.Bus != d.Bus) {
			panic("slogx: event " + d.Name + " defined twice")
		}
		registry[d.Name] = d
	}
}

// Registered returns the registered events by name.
func Registered() []*Def {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Def, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LogFallback is written when a log file cannot be opened.
var LogFallback = Def{Name: "log_fallback", Level: slog.LevelWarn}

func init() { Register(&LogFallback, &TLSHandshakeFailed) }

// Emit writes event d at its level to l with the given attributes (as
// slog's args). A security or bus event is passed on even when its level is
// filtered out of the main log.
func Emit(l *slog.Logger, d *Def, args ...any) {
	emit(context.Background(), l, d, "", args)
}

// EmitMsg is Emit with a message for people.
func EmitMsg(l *slog.Logger, d *Def, msg string, args ...any) {
	emit(context.Background(), l, d, msg, args)
}

// EmitContext is EmitMsg with a context, whose attributes
// (Config.Context) the line carries.
func EmitContext(ctx context.Context, l *slog.Logger, d *Def, msg string, args ...any) {
	emit(ctx, l, d, msg, args)
}

func emit(ctx context.Context, l *slog.Logger, d *Def, msg string, args []any) {
	h := l.Handler()
	if !d.Security && !d.Bus && !h.Enabled(ctx, d.Level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), d.Level, msg, pcs[0])
	r.AddAttrs(slog.Any(KeyEvent, defValue{d}))
	r.Add(args...)
	_ = h.Handle(ctx, r)
}

// Redacted is a value that never reaches a log: it is written as
// [REDACTED]. Wrap secrets in it when they must travel with other values.
type Redacted string

// LogValue implements slog.LogValuer.
func (Redacted) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// Short returns the first n bytes of id, for correlation fields such as a
// session id.
func Short(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}
