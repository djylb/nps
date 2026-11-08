package slogx

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TLSHandshakeFailed is a TLS handshake that net/http reported failing.
var TLSHandshakeFailed = Def{Name: "tls_handshake_failed", Level: slog.LevelDebug, Limit: "src_ip"}

// lineWriter turns each Write of a *log.Logger into one line.
type lineWriter struct {
	l     *slog.Logger
	level slog.Level
	parse func(l *slog.Logger, msg string, pc uintptr) bool
}

func (w lineWriter) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\r\n"))
	pc := outsideLog()
	if w.parse == nil || !w.parse(w.l, msg, pc) {
		ctx := context.Background()
		if w.l.Enabled(ctx, w.level) {
			r := slog.NewRecord(time.Now(), w.level, msg, pc)
			_ = w.l.Handler().Handle(ctx, r)
		}
	}
	return len(p), nil
}

// ownDir is the directory of this package's source, to tell its frames.
var ownDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file) + "/"
}()

// outsideLog returns the pc of the first caller outside the log package
// and this one, so source= names the code that logged.
func outsideLog() uintptr {
	var pcs [24]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		own := strings.HasPrefix(f.File, ownDir) && !strings.HasSuffix(f.File, "_test.go")
		if !strings.HasPrefix(f.Function, "log.") && !own {
			return f.PC + 1
		}
		if !more {
			return 0
		}
	}
}

// StdLogger returns a *log.Logger that writes each line to l at level.
func StdLogger(l *slog.Logger, level slog.Level) *log.Logger {
	return log.New(lineWriter{l: l, level: level}, "", 0)
}

// CaptureStdLog sends the standard library's default logger, which some
// libraries write to, to l at warn level.
func CaptureStdLog(l *slog.Logger) {
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(lineWriter{l: l, level: slog.LevelWarn})
}

// HTTPErrorLog returns a logger for http.Server.ErrorLog: TLS handshake
// failures become tls_handshake_failed events of listener, other lines are
// warnings.
func HTTPErrorLog(l *slog.Logger, listener string) *log.Logger {
	return log.New(lineWriter{l: l, level: slog.LevelWarn, parse: func(l *slog.Logger, msg string, pc uintptr) bool {
		return parseTLSHandshake(l, listener, msg, pc)
	}}, "", 0)
}

// parseTLSHandshake reads "http: TLS handshake error from <addr>: <err>".
func parseTLSHandshake(l *slog.Logger, listener, msg string, pc uintptr) bool {
	_, rest, ok := strings.Cut(msg, "TLS handshake error from ")
	if !ok {
		return false
	}
	addr, why, _ := strings.Cut(rest, ": ")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	emit(context.Background(), l, &TLSHandshakeFailed, "", pc, []any{"src_ip", host, "src_port", port, "listener", listener, "reason", tlsReason(why)})
	return true
}

var tlsReasons = [][2]string{
	{"eof", "eof"},
	{"timeout", "timeout"},
	{"i/o timeout", "timeout"},
	{"connection reset", "eof"},
	{"first record does not look like", "bad_record"},
	{"record overflow", "bad_record"},
	{"bad record", "bad_record"},
	{"no certificate", "no_cert"},
	{"no certificates", "no_cert"},
	{"unknown certificate", "no_cert"},
	{"unrecognized name", "unknown_sni"},
	{"server name", "unknown_sni"},
	{"protocol version", "version"},
	{"unsupported versions", "version"},
	{"cipher", "cipher"},
}

func tlsReason(s string) string {
	s = strings.ToLower(s)
	for _, p := range tlsReasons {
		if strings.Contains(s, p[0]) {
			return p[1]
		}
	}
	return "other"
}
