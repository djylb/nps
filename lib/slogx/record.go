package slogx

import (
	"log/slog"
	"time"
)

// Record is a line on its way to the routes: attributes resolved, groups
// flattened into dotted keys, event and module taken out into their fixed
// places. Encoders and outputs may read it; they must not keep it after
// returning, as it is reused.
type Record struct {
	Time   time.Time
	Level  slog.Level
	Module string
	// Event is the event name; Def its definition when the line was
	// written with Emit.
	Event string
	Def   *Def
	Attrs []slog.Attr
	Msg   string
	// Source is "file:line" of the call when Config.AddSource is set.
	Source string
	// Stack is the goroutine stack when Config.StackLevel is reached.
	Stack string
}

// Clone returns a copy of r that may be kept.
func (r *Record) Clone() Record {
	c := *r
	c.Attrs = append([]slog.Attr(nil), r.Attrs...)
	return c
}

// Attr returns the value of the attribute key and whether it is present.
func (r *Record) Attr(key string) (slog.Value, bool) {
	for _, a := range r.Attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return slog.Value{}, false
}

func reserved(k string) bool {
	switch k {
	case KeyTime, KeyLevel, KeyEvent, KeyModule, KeyMsg, KeySource, KeyStack, KeySuppressed:
		return true
	}
	return false
}

// defValue carries a *Def through slog as the value of the event key, so a
// record knows its definition without a lookup by name.
type defValue struct{ d *Def }

func (v defValue) LogValue() slog.Value { return slog.StringValue(v.d.Name) }

// add resolves a into r, flattening groups; the event of Emit goes to its
// fixed place.
func (r *Record) add(a slog.Attr, prefix string) {
	if a.Value.Kind() == slog.KindLogValuer && prefix == "" && a.Key == KeyEvent {
		if dv, ok := a.Value.Any().(defValue); ok {
			r.Def, r.Event = dv.d, dv.d.Name
			return
		}
	}
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
			r.add(g, p)
		}
		return
	}
	a.Key = prefix + a.Key
	if reserved(a.Key) {
		// Only Emit sets the event and only the logger the module, so the
		// level table and routing see what the line says. An attribute
		// cannot stand in for a fixed field.
		a.Key = "_" + a.Key
	}
	r.Attrs = append(r.Attrs, a)
}
