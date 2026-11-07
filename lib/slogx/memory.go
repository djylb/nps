package slogx

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Entry is a line kept by Memory, detached from the record it came from.
type Entry struct {
	Seq    uint64     `json:"seq"`
	Time   time.Time  `json:"time"`
	Level  slog.Level `json:"-"`
	LevelS string     `json:"level"`
	Module string     `json:"module,omitempty"`
	Event  string     `json:"event,omitempty"`
	Msg    string     `json:"msg,omitempty"`
	Attrs  []Field    `json:"attrs,omitempty"`
}

// Field is an attribute of an Entry, its value as text.
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Query selects entries. Zero fields select everything.
type Query struct {
	After    uint64     // entries with a larger Seq
	Since    time.Time  // at or after
	MinLevel slog.Level // at or above; zero is info, so set it for debug
	AnyLevel bool       // ignore MinLevel
	Modules  []string
	Events   []string
	Text     string // contained in msg or a value, case-insensitive
	Limit    int    // newest Limit entries; zero for all
}

func (q *Query) match(e *Entry) bool {
	if e.Seq <= q.After || (!q.Since.IsZero() && e.Time.Before(q.Since)) {
		return false
	}
	if !q.AnyLevel && e.Level < q.MinLevel {
		return false
	}
	if len(q.Modules) > 0 && !slices.Contains(q.Modules, e.Module) {
		return false
	}
	if len(q.Events) > 0 && !slices.Contains(q.Events, e.Event) {
		return false
	}
	if q.Text != "" {
		t := strings.ToLower(q.Text)
		found := strings.Contains(strings.ToLower(e.Msg), t)
		for _, f := range e.Attrs {
			found = found || strings.Contains(strings.ToLower(f.Value), t)
		}
		if !found {
			return false
		}
	}
	return true
}

// Memory keeps the last entries in memory for a viewer, such as a web page:
// Query reads them, Subscribe follows new ones. Use it as the sink of a
// Route (Route.Memory).
type Memory struct {
	mu   sync.Mutex
	buf  []Entry
	next int // where the next entry goes once buf is full
	seq  uint64
	subs map[*Subscription]struct{}
}

// NewMemory keeps up to size entries.
func NewMemory(size int) *Memory {
	return &Memory{buf: make([]Entry, 0, max(size, 1))}
}

func (m *Memory) add(r *Record) {
	e := Entry{Time: r.Time, Level: r.Level, LevelS: LevelName(r.Level), Module: r.Module, Event: r.Event, Msg: r.Msg}
	if len(r.Attrs) > 0 {
		e.Attrs = make([]Field, len(r.Attrs))
		for i, a := range r.Attrs {
			e.Attrs[i] = Field{Key: a.Key, Value: string(appendPlain(nil, a.Value))}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	e.Seq = m.seq
	if len(m.buf) < cap(m.buf) {
		m.buf = append(m.buf, e)
	} else {
		m.buf[m.next] = e
		m.next = (m.next + 1) % len(m.buf)
	}
	for s := range m.subs {
		if s.q.match(&e) {
			select {
			case s.c <- e:
			default:
				s.dropped++
			}
		}
	}
}

// appendPlain writes v as text without logfmt quoting.
func appendPlain(b []byte, v slog.Value) []byte {
	if v.Kind() == slog.KindString {
		return append(b, v.String()...)
	}
	if v.Kind() == slog.KindAny {
		return append(b, valueString(v)...)
	}
	return appendTextValue(b, v)
}

// Query returns the kept entries q selects, oldest first.
func (m *Memory) Query(q Query) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	n := len(m.buf)
	for i := range n {
		e := &m.buf[(m.next+i)%n]
		if q.match(e) {
			out = append(out, *e)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out
}

// Subscription follows new entries.
type Subscription struct {
	m       *Memory
	q       Query
	c       chan Entry
	dropped uint64
}

// C delivers entries; it closes when the subscription ends.
func (s *Subscription) C() <-chan Entry { return s.c }

// Dropped reports how many entries did not fit in the queue.
func (s *Subscription) Dropped() uint64 {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return s.dropped
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.subs[s]; ok {
		delete(s.m.subs, s)
		close(s.c)
	}
}

// Subscribe follows the entries q selects (After, Since and Limit are
// ignored) through a queue of size entries. An entry that does not fit is
// dropped for this subscriber and counted, so a slow viewer never slows
// logging. The subscription ends when ctx is done or Close is called.
func (m *Memory) Subscribe(ctx context.Context, q Query, size int) *Subscription {
	q.After, q.Since, q.Limit = 0, time.Time{}, 0
	s := &Subscription{m: m, q: q, c: make(chan Entry, max(size, 1))}
	m.mu.Lock()
	if m.subs == nil {
		m.subs = map[*Subscription]struct{}{}
	}
	m.subs[s] = struct{}{}
	m.mu.Unlock()
	if ctx.Done() != nil {
		context.AfterFunc(ctx, s.Close)
	}
	return s
}

// Text returns the kept entries q selects encoded with enc, for a plain
// download or an application that polls for lines.
func (m *Memory) Text(q Query, enc Encoder) string {
	var b []byte
	for _, e := range m.Query(q) {
		r := Record{Time: e.Time, Level: e.Level, Module: e.Module, Event: e.Event, Msg: e.Msg}
		for _, f := range e.Attrs {
			r.Attrs = append(r.Attrs, slog.String(f.Key, f.Value))
		}
		b = enc.Encode(b, &r)
	}
	return string(b)
}
