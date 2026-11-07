package slogx

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net"
	"strings"
)

// TLSHandshakeFailed is a TLS handshake that net/http reported failing.
var TLSHandshakeFailed = Def{Name: "tls_handshake_failed", Level: slog.LevelDebug, Limit: "src_ip"}

// lineWriter turns each Write of a *log.Logger into one line.
type lineWriter struct {
	l     *slog.Logger
	level slog.Level
	parse func(l *slog.Logger, msg string) bool
}

func (w lineWriter) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\r\n"))
	if w.parse == nil || !w.parse(w.l, msg) {
		w.l.Log(context.Background(), w.level, msg)
	}
	return len(p), nil
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
	return log.New(lineWriter{l: l, level: slog.LevelWarn, parse: func(l *slog.Logger, msg string) bool {
		return parseTLSHandshake(l, listener, msg)
	}}, "", 0)
}

// parseTLSHandshake reads "http: TLS handshake error from <addr>: <err>".
func parseTLSHandshake(l *slog.Logger, listener, msg string) bool {
	_, rest, ok := strings.Cut(msg, "TLS handshake error from ")
	if !ok {
		return false
	}
	addr, why, _ := strings.Cut(rest, ": ")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	Emit(l, &TLSHandshakeFailed, "src_ip", host, "src_port", port, "listener", listener, "reason", tlsReason(why))
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
