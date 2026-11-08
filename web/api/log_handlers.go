package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/djylb/slogx"
)

// logEntriesLimit bounds one answer of LogEntries; a viewer asks again with
// the last seq for more.
const logEntriesLimit = 1000

// LogEntries returns the log lines kept in memory (log_memory), oldest
// first: those after seq "after", at or above "level", of the
// comma-separated "module" and "event" lists, containing "text". "seq" in
// the answer is what to pass as after next time, so a viewer can follow the
// log by asking again.
func (a *App) LogEntries(c Context) {
	m := slogx.Default.Memory()
	if m == nil {
		c.RespondJSON(http.StatusOK, map[string]any{"enabled": false, "entries": []slogx.Entry{}, "seq": 0})
		return
	}
	q := slogx.Query{After: uint64(max(c.Int("after"), 0)), Text: c.String("text")}
	if s := c.String("level"); s != "" {
		lv, err := slogx.ParseLevel(s)
		if err != nil {
			c.RespondJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		q.MinLevel = lv
	}
	q.Modules = splitList(c.String("module"))
	q.Events = splitList(c.String("event"))
	q.Limit = c.Int("limit")
	if q.Limit <= 0 || q.Limit > logEntriesLimit {
		q.Limit = logEntriesLimit
	}
	entries := m.Query(q)
	seq := q.After
	if n := len(entries); n > 0 {
		seq = entries[n-1].Seq
	}
	c.RespondJSON(http.StatusOK, map[string]any{"enabled": true, "entries": entries, "seq": seq, "size": m.Size()})
}

// LogLevels returns the levels of the log as log_level reads them.
func (a *App) LogLevels(c Context) {
	c.RespondJSON(http.StatusOK, map[string]string{"level": slogx.Default.Levels().String()})
}

// LogOverride sets the level of "module" (empty for every module) to
// "level" for "ttl" seconds, for troubleshooting; a ttl of 0 ends it.
// log_level in nps.conf is not changed.
func (a *App) LogOverride(c Context) {
	module := strings.TrimSpace(c.String("module"))
	var err error
	if ttl := c.Int("ttl"); ttl <= 0 {
		slogx.Default.Levels().Clear(module)
	} else {
		var lv slog.Level
		if lv, err = slogx.ParseLevel(c.String("level")); err == nil {
			err = slogx.Default.Levels().Override(module, lv, time.Duration(ttl)*time.Second)
		}
	}
	if err != nil {
		c.RespondJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.RespondJSON(http.StatusOK, map[string]string{"level": slogx.Default.Levels().String()})
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
