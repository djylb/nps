package httpproxy

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// AccessRecord is one request to the domain proxy, given to
// HttpProxy.AccessLog when the request ends, refused ones included.
type AccessRecord struct {
	Time      time.Time // when the request arrived
	Route     string    // the domain of the host entry, or "http"/"https" when none matched
	Host      int       // the host entry id, 0 when none matched
	SrcIP     string
	Method    string
	Domain    string // the Host header
	Path      string
	Query     string // the raw query, without "?"
	Proto     string // such as HTTP/1.1
	Status    int    // 0 when the connection was dropped without a response
	Bytes     int64  // of the response body
	Duration  time.Duration
	Target    string // the backend host:port, empty when none was dialed
	Referer   string
	UserAgent string
}

// accessState is what the access log learns while a request is served.
type accessState struct {
	route  string
	host   int
	target string
}

type accessKey struct{}

func accessOf(ctx context.Context) *accessState {
	a, _ := ctx.Value(accessKey{}).(*accessState)
	return a
}

// accessEnabled reports whether requests are recorded at all; when not,
// they are served without any wrapper.
func (s *HttpServer) accessEnabled(ctx context.Context) bool {
	return s.AccessLog != nil || proxyLog.Enabled(ctx, logevent.HTTPAccess.Level)
}

// serveRecorded serves r through serve and records it when it ends, also
// when serve aborts the request with a panic.
func (s *HttpServer) serveRecorded(w http.ResponseWriter, r *http.Request, serve func(http.ResponseWriter, *http.Request)) {
	start := time.Now()
	a := &accessState{route: "http"}
	if r.TLS != nil {
		a.route = "https"
	}
	aw := &accessWriter{ResponseWriter: w}
	defer func() { s.writeAccess(r, a, aw.status, aw.bytes, start) }()
	serve(aw, r.WithContext(context.WithValue(r.Context(), accessKey{}, a)))
}

// writeAccess writes the http_access line and calls AccessLog.
func (s *HttpServer) writeAccess(r *http.Request, a *accessState, status int, bytes int64, start time.Time) {
	dur := time.Since(start)
	ip := srcIP(r.RemoteAddr)
	if proxyLog.Enabled(r.Context(), logevent.HTTPAccess.Level) {
		args := []any{"route", a.route}
		if a.host != 0 {
			args = append(args, "host", a.host)
		}
		args = append(args, "src_ip", ip, "method", r.Method, "domain", r.Host, "path", r.URL.Path,
			"proto", r.Proto, "status", status, "bytes", bytes, "dur_ms", dur.Milliseconds())
		if a.target != "" {
			args = append(args, "target", a.target)
		}
		slogx.Emit(r.Context(), proxyLog, &logevent.HTTPAccess, args...)
	}
	if s.AccessLog != nil {
		s.AccessLog(AccessRecord{
			Time: start, Route: a.route, Host: a.host, SrcIP: ip, Method: r.Method,
			Domain: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery, Proto: r.Proto,
			Status: status, Bytes: bytes, Duration: dur, Target: a.target,
			Referer: r.Referer(), UserAgent: r.UserAgent(),
		})
	}
}

// dialedConn is a backend connection that knows its target, so that a
// request on a reused connection still learns it.
type dialedConn struct {
	net.Conn
	target string
}

func (c *dialedConn) NetConn() net.Conn { return c.Conn }

// connTarget returns the target of a backend connection, possibly behind TLS.
func connTarget(c net.Conn) string {
	for c != nil {
		switch v := c.(type) {
		case *dialedConn:
			return v.target
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return ""
		}
	}
	return ""
}

// accessWriter records the status and size of a response for the access log.
// Unwrap lets http.ResponseController reach the flusher and hijacker of the
// writer it wraps.
type accessWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *accessWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *accessWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps streamed responses streaming through the wrapper.
func (w *accessWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// hijacker returns the hijacker of w, looking through an accessWriter.
func hijacker(w http.ResponseWriter) (http.Hijacker, bool) {
	if aw, ok := w.(*accessWriter); ok {
		w = aw.ResponseWriter
	}
	h, ok := w.(http.Hijacker)
	return h, ok
}

// hijacked records the status of a handshake written to a hijacked
// connection.
func hijacked(w http.ResponseWriter, status int) {
	if aw, ok := w.(*accessWriter); ok && aw.status == 0 {
		aw.status = status
	}
}
