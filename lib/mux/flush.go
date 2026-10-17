package mux

import (
	"io"
	"net"
	"sync"
)

// FlushWriter batches small writes to conn. The first write error is sticky:
// every later Write, Flush and Close returns it, so frames are never silently
// dropped after the underlying connection has failed.
type FlushWriter struct {
	conn    net.Conn
	mu      sync.Mutex
	buf     []byte
	maxSize int
	err     error
}

func NewFlushWriter(conn net.Conn) *FlushWriter {
	buf := windowBuff.Get()
	return &FlushWriter{
		conn:    conn,
		buf:     buf[:0],
		maxSize: cap(buf),
	}
}

// writeAll writes p completely to conn and records a failure as sticky.
func (w *FlushWriter) writeAll(p []byte) error {
	for len(p) > 0 {
		n, err := w.conn.Write(p)
		if err == nil && n <= 0 {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return err
		}
		p = p[n:]
	}
	return nil
}

func (w *FlushWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	n = len(p)
	b := len(w.buf)
	t := n + b
	if t > w.maxSize && b > 0 {
		err = w.writeAll(w.buf)
		w.buf = w.buf[:0]
		if err != nil {
			return 0, err
		}
	}
	if n > w.maxSize {
		if err = w.writeAll(p); err != nil {
			return 0, err
		}
		return n, nil
	}
	w.buf = append(w.buf, p...)
	return n, nil
}

func (w *FlushWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if len(w.buf) == 0 {
		return nil
	}
	err := w.writeAll(w.buf)
	w.buf = w.buf[:0]
	return err
}

func (w *FlushWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf == nil {
		return w.err
	}
	var err error
	if w.err == nil && len(w.buf) > 0 {
		err = w.writeAll(w.buf)
	}
	windowBuff.Put(w.buf)
	w.buf = nil
	if w.err == nil {
		w.err = net.ErrClosed
	}
	return err
}
