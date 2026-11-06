package mux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

var errOpenCanceled = errors.New("create connection canceled")

// OpenStream opens a stream to the peer, waiting until ctx's deadline or, if
// it has none, as long as NewConn.
func (s *Mux) OpenStream(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var timeout time.Duration
	if d, ok := ctx.Deadline(); ok {
		// A deadline passing while waiting ends the wait through ctx.
		timeout = time.Until(d) + time.Second
	}
	c, err := s.openConn(ctx.Done(), timeout)
	if err != nil {
		if errors.Is(err, errOpenCanceled) {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return c, nil
}

// AcceptStream returns the next stream the peer opened.
func (s *Mux) AcceptStream(ctx context.Context) (net.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closeChan:
		return nil, s.closedErr("accept error: the mux has closed")
	case conn, ok := <-s.newConnCh:
		if !ok || conn == nil {
			return nil, errors.New("accept error: the connection has been closed")
		}
		if s.IsClosed() {
			// both cases were ready; never hand out a stream of a closed mux
			_ = conn.Close()
			return nil, s.closedErr("accept error: the mux has closed")
		}
		return conn, nil
	}
}

// Closed reports whether the mux has closed.
func (s *Mux) Closed() bool { return s.IsClosed() }

// Reason says why the mux closed.
func (s *Mux) Reason() string { return s.CloseReason() }

// Logger receives the mux's diagnostics.
type Logger interface {
	Println(v ...any)
	Printf(format string, v ...any)
	Warn(format string, v ...any)
	Error(format string, v ...any)
}

type nopLogger struct{}

func (nopLogger) Println(...any)        {}
func (nopLogger) Printf(string, ...any) {}
func (nopLogger) Warn(string, ...any)   {}
func (nopLogger) Error(string, ...any)  {}

type loggerBox struct{ Logger }

type logHolder struct{ p atomic.Pointer[loggerBox] }

// logs is where the package logs; nothing is logged unless SetLogger says where.
var logs logHolder

func (h *logHolder) current() Logger {
	if b := h.p.Load(); b != nil {
		return b.Logger
	}
	return nopLogger{}
}

func (h *logHolder) Println(v ...any)               { h.current().Println(v...) }
func (h *logHolder) Printf(format string, v ...any) { h.current().Printf(format, v...) }
func (h *logHolder) Warn(format string, v ...any)   { h.current().Warn(format, v...) }
func (h *logHolder) Error(format string, v ...any)  { h.current().Error(format, v...) }

// SetLogger sets where the package logs; nil stops logging.
func SetLogger(l Logger) {
	if l == nil {
		logs.p.Store(nil)
		return
	}
	logs.p.Store(&loggerBox{l})
}

// errorDetails describes err and the addresses of c for logs.
func errorDetails(err error, c net.Conn) string {
	s := fmt.Sprintf("err=%v", err)
	if c == nil {
		return s
	}
	if a := c.LocalAddr(); a != nil {
		s += " local=" + a.String()
	}
	if a := c.RemoteAddr(); a != nil {
		s += " remote=" + a.String()
	}
	return s
}
