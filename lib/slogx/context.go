package slogx

import (
	"context"
	"log/slog"
	"runtime/pprof"
)

// PprofLabels returns the pprof labels ctx carries as attributes, for
// Config.Context: a component labelled for profiles and tracebacks (such
// as with pprof.Do) then carries the same names in its log lines. Labels
// are not secret-safe; keep secrets out of them.
func PprofLabels(ctx context.Context) []slog.Attr {
	var out []slog.Attr
	pprof.ForLabels(ctx, func(k, v string) bool {
		out = append(out, slog.String(k, v))
		return true
	})
	return out
}

// ContextAttrs combines context extractors for Config.Context.
func ContextAttrs(fns ...func(context.Context) []slog.Attr) func(context.Context) []slog.Attr {
	return func(ctx context.Context) []slog.Attr {
		var out []slog.Attr
		for _, f := range fns {
			out = append(out, f(ctx)...)
		}
		return out
	}
}
