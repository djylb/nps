// Package slogxtest records slogx lines for tests to assert on.
package slogxtest

import (
	"io"
	"log/slog"
	"sync"

	"github.com/djylb/nps/lib/slogx"
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

// Route returns a route that records into r: all lines at or above minLevel,
// whatever the module levels.
func (r *Recorder) Route(minLevel slog.Level) slogx.Route {
	return slogx.Route{Name: "slogxtest", Match: slogx.MinLevel(minLevel), Below: true, Func: r.add}
}

// New returns Logs that write nothing but to the returned Recorder, which
// takes every line at or above trace.
func New() (*slogx.Logs, *Recorder) {
	r := &Recorder{}
	l := slogx.New()
	if err := l.Configure(slogx.Config{Output: "off", Level: "off", Routes: []slogx.Route{r.Route(slogx.LevelTrace)}}); err != nil {
		panic(err)
	}
	return l, r
}

func (r *Recorder) add(rec *slogx.Record) {
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

// TB is the part of testing.TB that ToTest uses.
type TB interface {
	Output() io.Writer
	Cleanup(func())
}

// ToTest returns Logs that write every line, logfmt, to the output of t,
// where go test shows it with the test's own log; it closes when t ends.
func ToTest(t TB) *slogx.Logs {
	l := slogx.New()
	route := slogx.Route{Name: "test", Match: slogx.MinLevel(slogx.LevelTrace), Below: true, Output: slogx.WriterOutput(t.Output())}
	if err := l.Configure(slogx.Config{Output: "off", Level: "off", Routes: []slogx.Route{route}}); err != nil {
		panic(err)
	}
	t.Cleanup(l.Close)
	return l
}
