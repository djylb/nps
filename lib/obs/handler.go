// Package obs writes structured logs: one event per line, fixed fields
// first (time, level, event, module), then the event's own fields, then msg.
// The text form is logfmt; the JSON form has the same keys.
//
// A process sets up one Logs and modules take loggers from it with Module.
// Every line passes per-module level filtering; lines that carry an event
// defined with Def may also go to a security log, to a Sink, and be rate
// limited.
package obs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// LevelTrace is below debug, for per-packet detail.
const LevelTrace = slog.Level(-8)

// Keys of the fixed fields.
const (
	KeyEvent      = "event"
	KeyModule     = "module"
	KeySuppressed = "suppressed"
)

// Format is how lines are written.
type Format int

const (
	Text Format = iota // logfmt
	JSON
)

// output is one destination; lines to it are written whole under its lock.
type output struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *output) write(b []byte) {
	o.mu.Lock()
	_, _ = o.w.Write(b)
	o.mu.Unlock()
}

// line is a record about to be written, its attributes flattened.
type line struct {
	t      time.Time
	level  slog.Level
	event  string
	module string
	attrs  []slog.Attr
	msg    string
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 0, 512); return &b }}

func (l *line) encode(f Format) *[]byte {
	bp := bufPool.Get().(*[]byte)
	b := (*bp)[:0]
	if f == JSON {
		b = append(b, `{"time":`...)
		b = appendJSONString(b, l.t.UTC().Format("2006-01-02T15:04:05.000Z07:00"))
		b = append(b, `,"level":`...)
		b = appendJSONString(b, LevelName(l.level))
		if l.event != "" {
			b = append(b, `,"event":`...)
			b = appendJSONString(b, l.event)
		}
		if l.module != "" {
			b = append(b, `,"module":`...)
			b = appendJSONString(b, l.module)
		}
		for _, a := range l.attrs {
			b = append(b, ',')
			b = appendJSONString(b, a.Key)
			b = append(b, ':')
			b = appendJSONValue(b, a.Value)
		}
		if l.msg != "" {
			b = append(b, `,"msg":`...)
			b = appendJSONString(b, l.msg)
		}
		b = append(b, "}\n"...)
	} else {
		b = append(b, "time="...)
		b = l.t.UTC().AppendFormat(b, "2006-01-02T15:04:05.000Z07:00")
		b = append(b, " level="...)
		b = append(b, LevelName(l.level)...)
		if l.event != "" {
			b = append(b, " event="...)
			b = appendText(b, l.event)
		}
		if l.module != "" {
			b = append(b, " module="...)
			b = appendText(b, l.module)
		}
		for _, a := range l.attrs {
			b = append(b, ' ')
			b = appendKey(b, a.Key)
			b = append(b, '=')
			b = appendTextValue(b, a.Value)
		}
		if l.msg != "" {
			b = append(b, " msg="...)
			b = appendText(b, l.msg)
		}
		b = append(b, '\n')
	}
	*bp = b
	return bp
}

// LevelName returns the name a line carries for level.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "TRACE"
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

func appendKey(b []byte, k string) []byte {
	if k == "" {
		return append(b, '_')
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c <= ' ' || c == '=' || c == '"' || c >= 0x7f {
			c = '_'
		}
		b = append(b, c)
	}
	return b
}

func appendTextValue(b []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendText(b, v.String())
	case slog.KindInt64:
		return strconv.AppendInt(b, v.Int64(), 10)
	case slog.KindUint64:
		return strconv.AppendUint(b, v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.AppendFloat(b, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		return strconv.AppendBool(b, v.Bool())
	case slog.KindDuration:
		return strconv.AppendInt(b, v.Duration().Milliseconds(), 10)
	case slog.KindTime:
		return v.Time().UTC().AppendFormat(b, time.RFC3339)
	default:
		return appendText(b, valueString(v))
	}
}

func valueString(v slog.Value) string {
	a := v.Any()
	if e, ok := a.(error); ok {
		return e.Error()
	}
	return v.String()
}

// appendText writes s bare when it needs no quoting, else quoted with
// control characters escaped, so no input can start a line of its own.
func appendText(b []byte, s string) []byte {
	if s != "" && !needsQuote(s) {
		return append(b, s...)
	}
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				b = append(b, `�`...)
			} else {
				b = append(b, s[i:i+n]...)
			}
			i += n
			continue
		}
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, `\n`...)
		case '\r':
			b = append(b, `\r`...)
		case '\t':
			b = append(b, `\t`...)
		default:
			if c < ' ' || c == 0x7f {
				b = append(b, `\u00`...)
				b = append(b, "0123456789abcdef"[c>>4], "0123456789abcdef"[c&15])
			} else {
				b = append(b, c)
			}
		}
		i++
	}
	return append(b, '"')
}

func needsQuote(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '=' || c == '"' || c == '\\' || c >= 0x7f {
			return true
		}
	}
	return false
}

func appendJSONString(b []byte, s string) []byte {
	q, _ := json.Marshal(s)
	return append(b, q...)
}

func appendJSONValue(b []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendJSONString(b, v.String())
	case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration:
		return appendTextValue(b, v)
	case slog.KindTime:
		return appendJSONString(b, v.Time().UTC().Format(time.RFC3339))
	default:
		return appendJSONString(b, valueString(v))
	}
}

// handler is the slog.Handler of one module logger.
type handler struct {
	logs   *Logs
	module string
	attrs  []slog.Attr // already resolved, keys with group prefix
	group  string
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool {
	return h.logs.enabled(h.module, l)
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	ln := line{t: r.Time, level: r.Level, module: h.module, msg: r.Message}
	if ln.t.IsZero() {
		ln.t = time.Now()
	}
	ln.attrs = make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		ln.add(a, "")
	}
	r.Attrs(func(a slog.Attr) bool { ln.add(a, h.group); return true })
	h.logs.emit(&ln)
	return nil
}

// add resolves a, flattens groups, and takes event and module out of the
// attributes into their fixed places.
func (ln *line) add(a slog.Attr, prefix string) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			ln.add(g, p)
		}
		return
	}
	if prefix == "" {
		switch a.Key {
		case KeyEvent:
			ln.event = a.Value.String()
			return
		case KeyModule:
			ln.module = a.Value.String()
			return
		}
	}
	a.Key = prefix + a.Key
	ln.attrs = append(ln.attrs, a)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range as {
		a.Value = a.Value.Resolve()
		if h.group == "" && a.Key == KeyModule {
			n.module = a.Value.String()
			continue
		}
		if h.group != "" {
			a = slog.Group(h.group[:len(h.group)-1], a)
		}
		n.attrs = append(n.attrs, a)
	}
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.group = h.group + name + "."
	return &n
}
