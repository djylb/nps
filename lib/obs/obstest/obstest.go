// Package obstest records obs lines for tests to assert on.
package obstest

import (
	"log/slog"
	"sync"

	"github.com/djylb/nps/lib/obs"
)

// Line is a recorded line.
type Line struct {
	Level  slog.Level
	Module string
	Event  string
	Msg    string
	Attrs  map[string]string
}

// Recorder keeps every line its route receives.
type Recorder struct {
	mu    sync.Mutex
	lines []Line
}

// Route returns a route that records into r: all lines at or above min,
// whatever the module levels.
func (r *Recorder) Route(min slog.Level) obs.Route {
	return obs.Route{Name: "obstest", Match: obs.MinLevel(min), Below: true, Func: r.add}
}

// New returns Logs that write nothing but to the returned Recorder, which
// takes every line at or above trace.
func New() (*obs.Logs, *Recorder) {
	r := &Recorder{}
	l := obs.New()
	if err := l.Configure(obs.Config{Output: "off", Level: "off", Routes: []obs.Route{r.Route(obs.LevelTrace)}}); err != nil {
		panic(err)
	}
	return l, r
}

func (r *Recorder) add(rec *obs.Record) {
	ln := Line{Level: rec.Level, Module: rec.Module, Event: rec.Event, Msg: rec.Msg, Attrs: map[string]string{}}
	for _, a := range rec.Attrs {
		ln.Attrs[a.Key] = a.Value.String()
	}
	r.mu.Lock()
	r.lines = append(r.lines, ln)
	r.mu.Unlock()
}

// Lines returns the recorded lines.
func (r *Recorder) Lines() []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Line(nil), r.lines...)
}

// Events returns the recorded lines of event name.
func (r *Recorder) Events(name string) []Line {
	var out []Line
	for _, l := range r.Lines() {
		if l.Event == name {
			out = append(out, l)
		}
	}
	return out
}

// Reset forgets the recorded lines.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.lines = nil
	r.mu.Unlock()
}
