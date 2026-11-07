// Package logs forwards the printf-style calls not yet moved to obs. It
// goes away once every caller logs through an obs module logger.
package logs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/djylb/nps/lib/obs"
)

var legacy = obs.Module("nps")

func logf(l slog.Level, msg string, v []any) {
	if !legacy.Enabled(context.Background(), l) {
		return
	}
	legacy.Log(context.Background(), l, fmt.Sprintf(msg, v...))
}

func Trace(msg string, v ...any)  { logf(obs.LevelTrace, msg, v) }
func Debug(msg string, v ...any)  { logf(slog.LevelDebug, msg, v) }
func Info(msg string, v ...any)   { logf(slog.LevelInfo, msg, v) }
func Warn(msg string, v ...any)   { logf(slog.LevelWarn, msg, v) }
func Error(msg string, v ...any)  { logf(slog.LevelError, msg, v) }
func Printf(msg string, v ...any) { logf(slog.LevelInfo, msg, v) }
func Println(v ...any)            { legacy.Info(fmt.Sprint(v...)) }
