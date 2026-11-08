package slogx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LevelTrace is below debug, for per-packet detail.
const LevelTrace = slog.Level(-8)

// LevelOff is above every level: nothing is written.
const LevelOff = slog.Level(1 << 20)

// Levels is the level table of a Logs: a default and per-module levels,
// plus temporary raises that expire. It is safe for concurrent use; reading
// a level costs one atomic load.
type Levels struct {
	cur atomic.Pointer[levelTable]
	mu  sync.Mutex // writers
	// base is what Set gave; raises are applied on top of it.
	base   levelTable
	raises map[string]raise
	timer  *time.Timer
	// stopped: the Logs closed; no timer runs again.
	stopped bool
	// MaxRaises bounds how many modules may be raised at once; Raise
	// refuses more. Zero is 1024.
	MaxRaises int
}

type raise struct {
	level slog.Level
	until time.Time
}

type levelTable struct {
	def     slog.Level
	modules map[string]slog.Level
	min     slog.Level // the lowest level of all, for a quick Enabled
}

func (t *levelTable) of(module string) slog.Level {
	if l, ok := t.modules[module]; ok {
		return l
	}
	return t.def
}

func newLevels() *Levels {
	l := &Levels{base: levelTable{def: slog.LevelInfo}}
	l.publish()
	return l
}

// Of returns the level of module now.
func (l *Levels) Of(module string) slog.Level { return l.cur.Load().of(module) }

// Set replaces the default and per-module levels as spec says,
// "info,db=debug,net=warn". Raises stay until they expire.
func (l *Levels) Set(spec string) error {
	t, err := parseLevels(spec)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.base = t
	l.publish()
	return nil
}

// String returns the levels as Set reads them, without raises.
func (l *Levels) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return formatLevels(l.base)
}

// ErrTooManyRaises is returned by Raise beyond MaxRaises modules.
var ErrTooManyRaises = errors.New("slogx: too many raised modules")

// Raise lowers the threshold of module ("" for the default and so every
// module) to level for ttl, after which it returns to what Set gave. A raise
// never makes a module quieter than Set made it. A non-positive ttl ends the
// raise, as Lower does.
func (l *Levels) Raise(module string, level slog.Level, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return errors.New("slogx: logs closed")
	}
	if ttl <= 0 {
		delete(l.raises, module)
		l.publish()
		return nil
	}
	limit := l.MaxRaises
	if limit <= 0 {
		limit = 1024
	}
	if _, ok := l.raises[module]; !ok && len(l.raises) >= limit {
		return ErrTooManyRaises
	}
	if l.raises == nil {
		l.raises = map[string]raise{}
	}
	l.raises[module] = raise{level: level, until: time.Now().Add(ttl)}
	l.publish()
	return nil
}

// Lower ends the raise of module now.
func (l *Levels) Lower(module string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.raises, module)
	l.publish()
}

// publish builds the effective table; l.mu is held. A raise of the
// default ("") applies to every module; a module's own raise to it alone.
func (l *Levels) publish() {
	now := time.Now()
	defRaise := LevelOff
	var next time.Time
	for m, r := range l.raises {
		if !now.Before(r.until) {
			delete(l.raises, m)
			continue
		}
		if next.IsZero() || r.until.Before(next) {
			next = r.until
		}
		if m == "" {
			defRaise = r.level
		}
	}
	t := levelTable{def: min(l.base.def, defRaise), modules: make(map[string]slog.Level, len(l.base.modules)+len(l.raises))}
	for m, v := range l.base.modules {
		t.modules[m] = min(v, defRaise)
	}
	for m, r := range l.raises {
		if m != "" {
			base, ok := l.base.modules[m]
			if !ok {
				base = l.base.def
			}
			t.modules[m] = min(base, defRaise, r.level)
		}
	}
	t.min = t.def
	for _, v := range t.modules {
		t.min = min(t.min, v)
	}
	l.cur.Store(&t)
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if !next.IsZero() && !l.stopped {
		l.timer = time.AfterFunc(time.Until(next), func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if !l.stopped {
				l.publish()
			}
		})
	}
}

// setBase is Set with a parsed table.
func (l *Levels) setBase(t levelTable) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.base = t
	l.publish()
}

// stop ends the expiry timer and the raises for good.
func (l *Levels) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	l.raises = nil
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.publish()
}

// ServeHTTP shows the levels on GET, and on PUT or POST takes a JSON body
// {"level": "info,net=debug"} to Set, or {"module": "net", "level":
// "debug", "ttl": "10m"} to Raise. Mount it behind the application's own
// authorization.
func (l *Levels) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	type state struct {
		Level  string            `json:"level"`
		Raises map[string]string `json:"raises,omitempty"`
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut, http.MethodPost:
		// A JSON body cannot be sent cross-site without a preflight, which
		// keeps a browser of an authorized user from being used (CSRF).
		if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		var req struct {
			Module *string `json:"module"`
			Level  string  `json:"level"`
			TTL    string  `json:"ttl"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var err error
		switch {
		case strings.TrimSpace(req.Level) == "":
			err = errors.New("level is required")
		case req.Module == nil:
			err = l.Set(req.Level)
		default:
			var lv slog.Level
			var ttl time.Duration
			if lv, err = ParseLevel(req.Level); err == nil {
				if ttl, err = time.ParseDuration(req.TTL); err == nil && ttl <= 0 {
					err = errors.New("ttl must be positive")
				}
			}
			if err == nil {
				err = l.Raise(*req.Module, lv, ttl)
			}
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		w.Header().Set("Allow", "GET, PUT, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	l.mu.Lock()
	st := state{Level: formatLevels(l.base)}
	for m, rs := range l.raises {
		if st.Raises == nil {
			st.Raises = map[string]string{}
		}
		st.Raises[m] = strings.ToLower(LevelName(rs.level)) + " until " + rs.until.UTC().Format(time.RFC3339)
	}
	l.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

// ParseLevel reads one level name: trace, debug, info, warn, error, off,
// and the syslog-style names and numbers earlier configurations used.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "7":
		return LevelTrace, nil
	case "debug", "6":
		return slog.LevelDebug, nil
	case "", "info", "notice", "informational", "5":
		return slog.LevelInfo, nil
	case "warn", "warning", "4":
		return slog.LevelWarn, nil
	case "error", "fatal", "panic", "critical", "alert", "emergency", "3", "2", "1":
		return slog.LevelError, nil
	case "off", "disabled", "none", "0":
		return LevelOff, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

func parseLevels(spec string) (levelTable, error) {
	t := levelTable{def: slog.LevelInfo}
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		mod, name, ok := strings.Cut(part, "=")
		if !ok {
			l, err := ParseLevel(part)
			if err != nil {
				return levelTable{}, err
			}
			t.def = l
			continue
		}
		l, err := ParseLevel(name)
		if err != nil {
			return levelTable{}, err
		}
		mod = strings.TrimSpace(mod)
		if mod == "" {
			return levelTable{}, errors.New("log level without a module: " + part)
		}
		if t.modules == nil {
			t.modules = map[string]slog.Level{}
		}
		t.modules[mod] = l
	}
	return t, nil
}

func levelWord(l slog.Level) string {
	if l >= LevelOff {
		return "off"
	}
	return strings.ToLower(LevelName(l))
}

func formatLevels(t levelTable) string {
	parts := make([]string, 0, 1+len(t.modules))
	parts = append(parts, levelWord(t.def))
	mods := make([]string, 0, len(t.modules))
	for m := range t.modules {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		parts = append(parts, m+"="+levelWord(t.modules[m]))
	}
	return strings.Join(parts, ",")
}
