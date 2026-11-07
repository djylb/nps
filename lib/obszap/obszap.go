// Package obszap gives libraries that log with zap (certmagic, acmez) a
// *zap.Logger whose lines go to a *slog.Logger. Their lines below Min are
// written at debug.
package obszap

import (
	"context"
	"log/slog"
	"math"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New returns a zap logger writing to l; zap levels below min become debug.
func New(l *slog.Logger, min zapcore.Level) *zap.Logger {
	return zap.New(&core{l: l, min: min})
}

type core struct {
	l      *slog.Logger
	min    zapcore.Level
	fields []zapcore.Field
}

func (c *core) level(z zapcore.Level) slog.Level {
	if z < c.min {
		return slog.LevelDebug
	}
	switch {
	case z >= zapcore.ErrorLevel:
		return slog.LevelError
	case z >= zapcore.WarnLevel:
		return slog.LevelWarn
	case z >= zapcore.InfoLevel:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

func (c *core) Enabled(z zapcore.Level) bool {
	return c.l.Enabled(context.Background(), c.level(z))
}

func (c *core) With(fs []zapcore.Field) zapcore.Core {
	n := *c
	n.fields = append(append([]zapcore.Field(nil), c.fields...), fs...)
	return &n
}

func (c *core) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c *core) Write(e zapcore.Entry, fs []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fs {
		f.AddTo(enc)
	}
	r := slog.NewRecord(e.Time, c.level(e.Level), e.Message, 0)
	if e.LoggerName != "" {
		r.AddAttrs(slog.String("logger", e.LoggerName))
	}
	for k, v := range enc.Fields {
		r.AddAttrs(attr(k, v))
	}
	return c.l.Handler().Handle(context.Background(), r)
}

func attr(k string, v any) slog.Attr {
	switch x := v.(type) {
	case string:
		return slog.String(k, x)
	case int64:
		return slog.Int64(k, x)
	case int:
		return slog.Int(k, x)
	case uint64:
		if x <= math.MaxInt64 {
			return slog.Int64(k, int64(x))
		}
		return slog.Uint64(k, x)
	case float64:
		return slog.Float64(k, x)
	case bool:
		return slog.Bool(k, x)
	case time.Duration:
		return slog.Duration(k, x)
	case time.Time:
		return slog.Time(k, x)
	case error:
		return slog.String(k, x.Error())
	default:
		return slog.Any(k, x)
	}
}

func (c *core) Sync() error { return nil }
