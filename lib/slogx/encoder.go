package slogx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"
	"unicode/utf8"
)

// Encoder turns a record into bytes appended to dst, one line ending in a
// newline. Every encoder writes the fixed fields first, in the order time,
// level, event, module, then the record's attributes, then msg, source and
// stack.
type Encoder interface {
	Encode(dst []byte, r *Record) []byte
}

// Format names a built-in encoder.
type Format int

const (
	// Text is logfmt: key=value pairs, values quoted when needed.
	Text Format = iota
	// JSON is one JSON object per line.
	JSON
	// Pretty is for people at a terminal: aligned, optionally coloured. It
	// is not a format to parse.
	Pretty
)

// Encoder returns the built-in encoder of f; color applies to Pretty.
func (f Format) Encoder(color bool) Encoder {
	switch f {
	case JSON:
		return JSONEncoder{}
	case Pretty:
		return PrettyEncoder{Color: color}
	default:
		return TextEncoder{}
	}
}

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

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

// TextEncoder writes logfmt.
type TextEncoder struct{}

func (TextEncoder) Encode(b []byte, r *Record) []byte {
	b = append(b, "time="...)
	b = r.Time.UTC().AppendFormat(b, timeLayout)
	b = append(b, " level="...)
	b = append(b, LevelName(r.Level)...)
	if r.Event != "" {
		b = append(b, " event="...)
		b = appendText(b, r.Event)
	}
	if r.Module != "" {
		b = append(b, " module="...)
		b = appendText(b, r.Module)
	}
	for _, a := range r.Attrs {
		b = append(b, ' ')
		b = appendKey(b, a.Key)
		b = append(b, '=')
		b = appendTextValue(b, a.Value)
	}
	if r.Msg != "" {
		b = append(b, " msg="...)
		b = appendText(b, r.Msg)
	}
	if r.Source != "" {
		b = append(b, " source="...)
		b = appendText(b, r.Source)
	}
	if r.Stack != "" {
		b = append(b, " stack="...)
		b = appendText(b, r.Stack)
	}
	return append(b, '\n')
}

// JSONEncoder writes one JSON object per line, with the keys of
// TextEncoder; numbers and booleans keep their type.
type JSONEncoder struct{}

func (JSONEncoder) Encode(b []byte, r *Record) []byte {
	b = append(b, `{"time":"`...)
	b = r.Time.UTC().AppendFormat(b, timeLayout)
	b = append(b, `","level":"`...)
	b = append(b, LevelName(r.Level)...)
	b = append(b, '"')
	str := func(k, v string) {
		if v != "" {
			b = append(b, ',', '"')
			b = append(b, k...)
			b = append(b, `":`...)
			b = appendJSONString(b, v)
		}
	}
	str(KeyEvent, r.Event)
	str(KeyModule, r.Module)
	for _, a := range r.Attrs {
		b = append(b, ',')
		b = appendJSONString(b, a.Key)
		b = append(b, ':')
		b = appendJSONValue(b, a.Value)
	}
	str("msg", r.Msg)
	str("source", r.Source)
	str("stack", r.Stack)
	return append(b, "}\n"...)
}

// PrettyEncoder writes "15:04:05.000 WARN  module event msg key=value".
type PrettyEncoder struct {
	Color bool
}

func (e PrettyEncoder) Encode(b []byte, r *Record) []byte {
	b = r.Time.Local().AppendFormat(b, "15:04:05.000")
	b = append(b, ' ')
	name := LevelName(r.Level)
	if e.Color {
		b = append(b, levelColor(r.Level)...)
	}
	b = append(b, name...)
	if e.Color {
		b = append(b, "\x1b[0m"...)
	}
	for i := len(name); i < 6; i++ {
		b = append(b, ' ')
	}
	if r.Module != "" {
		b = e.dim(b, "["+r.Module+"] ")
	}
	if r.Event != "" {
		if e.Color {
			b = append(b, "\x1b[1m"...)
		}
		b = append(b, r.Event...)
		if e.Color {
			b = append(b, "\x1b[0m"...)
		}
		b = append(b, ' ')
	}
	b = append(b, sanitize(r.Msg)...)
	for _, a := range r.Attrs {
		b = append(b, ' ')
		b = e.dim(b, a.Key+"=")
		b = appendTextValue(b, a.Value)
	}
	if r.Source != "" {
		b = append(b, ' ')
		b = e.dim(b, r.Source)
	}
	if r.Stack != "" {
		b = append(b, "\n\t"...)
		b = append(b, sanitizeStack(r.Stack)...)
	}
	return append(b, '\n')
}

func (e PrettyEncoder) dim(b []byte, s string) []byte {
	if !e.Color {
		return append(b, sanitize(s)...)
	}
	b = append(b, "\x1b[2m"...)
	b = append(b, sanitize(s)...)
	return append(b, "\x1b[0m"...)
}

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "\x1b[31m"
	case l >= slog.LevelWarn:
		return "\x1b[33m"
	case l >= slog.LevelInfo:
		return "\x1b[32m"
	default:
		return "\x1b[90m"
	}
}

// sanitize keeps s on one line and free of terminal escapes.
func sanitize(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] == 0x7f {
			q := appendText(nil, s)
			return string(q[1 : len(q)-1])
		}
	}
	return s
}

func sanitizeStack(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n':
			out = append(out, '\n', '\t')
		case c < ' ' && c != '\t' || c == 0x7f:
			out = append(out, '?')
		default:
			out = append(out, c)
		}
	}
	return string(out)
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
		return v.Time().UTC().AppendFormat(b, timeLayout)
	default:
		return appendText(b, valueString(v))
	}
}

func valueString(v slog.Value) string {
	if e, ok := v.Any().(error); ok {
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

// appendJSONString writes s as a JSON string: quotes, backslashes and
// control characters escaped, invalid UTF-8 replaced, and U+2028 and U+2029
// escaped so the line is also safe inside JavaScript.
func appendJSONString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= ' ' && c != '"' && c != '\\' && c != 0x7f {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"', '\\':
				b = append(b, '\\', c)
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			}
			i++
			start = i
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			b = append(b, s[start:i]...)
			b = append(b, `\ufffd`...)
			i += n
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hex[r&15])
			i += n
			start = i
			continue
		}
		i += n
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

func appendJSONValue(b []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendJSONString(b, v.String())
	case slog.KindInt64, slog.KindUint64, slog.KindBool, slog.KindDuration:
		return appendTextValue(b, v)
	case slog.KindFloat64:
		f := v.Float64()
		if f != f || f > 1e308 || f < -1e308 {
			return appendJSONString(b, strconv.FormatFloat(f, 'g', -1, 64))
		}
		return appendTextValue(b, v)
	case slog.KindTime:
		b = append(b, '"')
		b = v.Time().UTC().AppendFormat(b, time.RFC3339Nano)
		return append(b, '"')
	default:
		if m, ok := v.Any().(json.Marshaler); ok {
			if j, err := m.MarshalJSON(); err == nil {
				var c bytes.Buffer
				if json.Compact(&c, j) == nil {
					return append(b, c.Bytes()...)
				}
			}
		}
		return appendJSONString(b, valueString(v))
	}
}
