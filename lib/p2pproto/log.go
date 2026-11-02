package p2pproto

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/djylb/nps/lib/logs"
)

// NewLogger returns a logger that writes the hole punching library's log
// records to the nps log, each line starting with prefix and followed by
// its attributes as key=value.
func NewLogger(prefix string) *slog.Logger { return slog.New(logHandler{prefix: prefix}) }

type logHandler struct {
	prefix string
	attrs  []slog.Attr
}

func (logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(h.prefix)
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	switch {
	case r.Level >= slog.LevelError:
		logs.Error("%s", b.String())
	case r.Level >= slog.LevelWarn:
		logs.Warn("%s", b.String())
	case r.Level >= slog.LevelInfo:
		logs.Info("%s", b.String())
	case r.Level >= slog.LevelDebug:
		logs.Debug("%s", b.String())
	default:
		logs.Trace("%s", b.String())
	}
	return nil
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{prefix: h.prefix, attrs: append(slices.Clone(h.attrs), attrs...)}
}

func (h logHandler) WithGroup(string) slog.Handler { return h }
