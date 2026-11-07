package obs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Def defines an event: its name, level and where else it goes. An
// application declares its events as package variables and registers them
// with Register, so that the log, the security log and the bus all take the
// event from one definition.
type Def struct {
	Name  string
	Level slog.Level
	// Security events also go to Config.SecurityFile.
	Security bool
	// Bus events also go to the Sink.
	Bus bool
	// Limit, when set, is the field whose value keys rate limiting: one line
	// per event and value per LimitWindow, the next one carrying how many
	// were suppressed. "*" limits the event as a whole.
	Limit string
}

// LimitWindow is how long a limited event stays quiet for one key.
const LimitWindow = time.Minute

var (
	registryMu sync.RWMutex
	registry   = map[string]*Def{}
)

// Register makes defs known by name. Registering a name twice with a
// different definition panics: one name, one definition.
func Register(defs ...*Def) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, d := range defs {
		if old, ok := registry[d.Name]; ok && old != d {
			panic("obs: event " + d.Name + " defined twice")
		}
		registry[d.Name] = d
	}
}

// Registered returns the registered events.
func Registered() []*Def {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Def, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	return out
}

func lookup(name string) *Def {
	if name == "" {
		return nil
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// LogFallback is written when a log file cannot be opened or written.
var LogFallback = Def{Name: "log_fallback", Level: slog.LevelWarn}

func init() { Register(&LogFallback) }

// Emit writes event d at its level to l with the given attributes (as
// slog's args). A security or bus event is passed on even when its level is
// filtered out of the main log.
func Emit(l *slog.Logger, d *Def, args ...any) {
	EmitMsg(l, d, "", args...)
}

// EmitMsg is Emit with a message for people.
func EmitMsg(l *slog.Logger, d *Def, msg string, args ...any) {
	ctx := context.Background()
	h := l.Handler()
	if !h.Enabled(ctx, d.Level) && !d.Security && !d.Bus {
		return
	}
	r := slog.NewRecord(time.Now(), d.Level, msg, 0)
	r.AddAttrs(slog.String(KeyEvent, d.Name))
	r.Add(args...)
	_ = h.Handle(ctx, r)
}

// limiter keeps, per event and key, when the last line was written and how
// many were suppressed since.
type limiter struct {
	mu   sync.Mutex
	seen map[string]*limitSlot
}

type limitSlot struct {
	at         time.Time
	suppressed uint64
}

// allow reports whether the line may be written and how many lines were
// suppressed before it.
func (l *limiter) allow(d *Def, attrs []slog.Attr, now time.Time) (uint64, bool) {
	key := d.Name
	if d.Limit != "*" {
		for _, a := range attrs {
			if a.Key == d.Limit {
				key += "\x00" + a.Value.String()
				break
			}
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]*limitSlot{}
	}
	s, ok := l.seen[key]
	if ok && now.Sub(s.at) < LimitWindow {
		s.suppressed++
		return 0, false
	}
	if !ok {
		if len(l.seen) >= 4096 {
			for k, v := range l.seen {
				if now.Sub(v.at) >= LimitWindow {
					delete(l.seen, k)
				}
			}
		}
		s = &limitSlot{}
		l.seen[key] = s
	}
	n := s.suppressed
	s.at, s.suppressed = now, 0
	return n, true
}

// Redacted is a value that never reaches a log: it is written as
// [REDACTED]. Wrap secrets in it when they must travel with other values.
type Redacted string

// LogValue implements slog.LogValuer.
func (Redacted) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// Short returns the first n characters of id, for correlation fields such
// as a session id.
func Short(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}
