package obs

import (
	"log/slog"
)

// Match decides whether a route takes a record. lv is the level table of
// the Logs.
type Match func(r *Record, lv *Levels) bool

// AtLevels takes records at or above the level of their module: the main
// log.
func AtLevels(r *Record, lv *Levels) bool { return r.Level >= lv.Of(r.Module) }

// SecurityEvents takes the events defined as security events, whatever the
// levels.
func SecurityEvents(r *Record, _ *Levels) bool { return r.Def != nil && r.Def.Security }

// BusEvents takes the events defined as bus events, whatever the levels.
func BusEvents(r *Record, _ *Levels) bool { return r.Def != nil && r.Def.Bus }

// MinLevel takes records at or above l, whatever the module levels.
func MinLevel(l slog.Level) Match {
	return func(r *Record, _ *Levels) bool { return r.Level >= l }
}

// AnyOf takes what any of ms takes.
func AnyOf(ms ...Match) Match {
	return func(r *Record, lv *Levels) bool {
		for _, m := range ms {
			if m(r, lv) {
				return true
			}
		}
		return false
	}
}

// AllOf takes what all of ms take.
func AllOf(ms ...Match) Match {
	return func(r *Record, lv *Levels) bool {
		for _, m := range ms {
			if !m(r, lv) {
				return false
			}
		}
		return true
	}
}

// Route sends the records Match takes to one destination: encoded to
// Output, kept in Memory, or handed to Func. Exactly one of them is set.
type Route struct {
	Name  string
	Match Match // nil is AtLevels
	// Below makes the route see records below the level table, for a Match
	// that asks for more than the main log does. Lines are then built even
	// when no other route wants them, which costs.
	Below bool

	Encoder Encoder // nil is TextEncoder
	Output  Output
	Memory  *Memory
	// Func receives the record; it must not keep it or block.
	Func func(*Record)
}

func (rt *Route) takes(r *Record, lv *Levels) bool {
	if rt.Match == nil {
		return AtLevels(r, lv)
	}
	return rt.Match(r, lv)
}

func (rt *Route) deliver(r *Record, buf *[]byte) {
	switch {
	case rt.Output != nil:
		enc := rt.Encoder
		if enc == nil {
			enc = TextEncoder{}
		}
		*buf = enc.Encode((*buf)[:0], r)
		_ = rt.Output.Write(*buf)
	case rt.Memory != nil:
		rt.Memory.add(r)
	case rt.Func != nil:
		rt.Func(r)
	}
}
