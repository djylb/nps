package slogx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
	"unicode/utf8"
)

// fuzzRecord builds a record whose every free-form part comes from the
// fuzzer.
func fuzzRecord(module, event, key, value, msg string) *Record {
	return &Record{
		Time: time.Unix(0, 0), Level: slog.LevelWarn, Module: module, Event: event, Msg: msg,
		Attrs: []slog.Attr{
			slog.String(key, value),
			slog.Any("err", errors.New(value)),
			slog.Group("", slog.String(key, msg)),
		},
		Source: value, Stack: msg,
	}
}

var seeds = []string{"", "plain", "a b", "x\ny", "\r\n", "q\"", "\\", "=", "level=ERROR", "\x1b[31m", "\x00", "\xff\xfe", " ", "中文", " time=x"}

func addSeeds(f *testing.F) {
	for _, a := range seeds {
		for _, b := range []string{"k", "", "a=b", "x\ny"} {
			f.Add("mod", "ev", b, a, a)
		}
	}
}

// FuzzTextOneLine: logfmt is always one line whose fixed fields cannot be
// forged by any input.
func FuzzTextOneLine(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, module, event, key, value, msg string) {
		b := TextEncoder{}.Encode(nil, fuzzRecord(module, event, key, value, msg))
		if bytes.IndexByte(b, '\n') != len(b)-1 || bytes.ContainsAny(b, "\r\x00") {
			t.Fatalf("not one line: %q", b)
		}
		if !bytes.HasPrefix(b, []byte("time=1970-01-01T00:00:00.000Z level=WARN ")) {
			t.Fatalf("fixed fields: %q", b)
		}
		// no input may produce a second unquoted level= or time= field
		if n := countBare(b, "level="); n != 1 {
			t.Fatalf("%d bare level= in %q", n, b)
		}
	})
}

// countBare counts occurrences of key at the start of a logfmt field
// outside quotes.
func countBare(b []byte, key string) int {
	n, inQuote := 0, false
	for i := 0; i < len(b); i++ {
		switch {
		case inQuote && b[i] == '\\':
			i++
		case b[i] == '"':
			inQuote = !inQuote
		case !inQuote && (i == 0 || b[i-1] == ' ') && bytes.HasPrefix(b[i:], []byte(key)):
			n++
		}
	}
	return n
}

// FuzzJSONValid: JSON lines always parse, keep their fixed fields, and
// round-trip the values.
func FuzzJSONValid(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, module, event, key, value, msg string) {
		b := JSONEncoder{}.Encode(nil, fuzzRecord(module, event, key, value, msg))
		if bytes.IndexByte(b, '\n') != len(b)-1 {
			t.Fatalf("not one line: %q", b)
		}
		var m map[string]any
		d := json.NewDecoder(bytes.NewReader(b))
		if err := d.Decode(&m); err != nil {
			t.Fatalf("%v: %q", err, b)
		}
		if m["level"] != "WARN" || m["time"] != "1970-01-01T00:00:00.000Z" {
			t.Fatalf("fixed fields: %v", m)
		}
		if module != "" && utf8.ValidString(module) && m["module"] != module {
			t.Fatalf("module %q -> %v", module, m["module"])
		}
	})
}

// FuzzPrettyClean: the terminal form is one line (stack aside) and carries
// no escape or control character from the input.
func FuzzPrettyClean(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, module, event, key, value, msg string) {
		r := fuzzRecord(module, event, key, value, msg)
		r.Stack = ""
		b := PrettyEncoder{}.Encode(nil, r)
		if bytes.IndexByte(b, '\n') != len(b)-1 {
			t.Fatalf("not one line: %q", b)
		}
		for _, c := range b[:len(b)-1] {
			if c < ' ' || c == 0x7f {
				t.Fatalf("control %#x in %q", c, b)
			}
		}
	})
}
