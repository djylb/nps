// Package obs is structured logging on log/slog: one line per event, fixed
// fields first (time, level, event, module), then the event's own fields,
// then msg. It adds to slog what an application needs around it: logfmt,
// JSON and terminal encoders; per-module levels with temporary raises;
// event definitions shared by the log, a security log and an event bus;
// rate limiting and sampling; routes to files, rotating files, an
// in-memory viewer and callbacks; and capture of net/http and the standard
// library's log.
//
// Callers only ever hold a *slog.Logger, from Logs.Module.
package obs

import (
	"context"
	"log/slog"
	"time"
)

// handler is the slog.Handler of a module logger.
type handler struct {
	logs   *Logs
	module string
	attrs  []slog.Attr // with group prefixes applied
	group  string      // "a.b." for WithGroup("a").WithGroup("b")
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool {
	return h.logs.enabled(h.module, l)
}

func (h *handler) Handle(ctx context.Context, sr slog.Record) error {
	p := h.logs.pipe.Load()
	r := Record{Time: sr.Time, Level: sr.Level, Module: h.module, Msg: sr.Message}
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	r.Attrs = make([]slog.Attr, 0, len(h.attrs)+sr.NumAttrs()+2)
	if p.ctxAttrs != nil && ctx != nil {
		for _, a := range p.ctxAttrs(ctx) {
			r.add(a, "")
		}
	}
	for _, a := range h.attrs {
		r.add(a, "")
	}
	sr.Attrs(func(a slog.Attr) bool {
		if h.group != "" && a.Key == KeyEvent {
			// an event set inside a group is still the event
			r.add(a, "")
			return true
		}
		r.add(a, h.group)
		return true
	})
	if p.addSource {
		r.Source = source(sr.PC)
	}
	if p.stackFrom != nil && r.Level >= *p.stackFrom {
		r.Stack = stack()
	}
	h.logs.dispatch(p, &r)
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range as {
		if h.group == "" && a.Key == KeyModule {
			n.module = a.Value.Resolve().String()
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
