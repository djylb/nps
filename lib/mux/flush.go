package mux

import (
	"net"
	"sync"
)

type FlushWriter struct {
	conn    net.Conn
	mu      sync.Mutex
	buf     []byte
	maxSize int
}

func NewFlushWriter(conn net.Conn) *FlushWriter {
	buf := windowBuff.Get()
	return &FlushWriter{
		conn:    conn,
		buf:     buf[:0],
		maxSize: cap(buf),
	}
}

func (w *FlushWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n = len(p)
	b := len(w.buf)
	t := n + b
	if t > w.maxSize && b > 0 {
		_, _ = w.conn.Write(w.buf)
		w.buf = w.buf[:0]
	}
	if n > w.maxSize {
		return w.conn.Write(p)
	}
	w.buf = append(w.buf, p...)
	return n, err
}

func (w *FlushWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	_, err := w.conn.Write(w.buf)
	w.buf = w.buf[:0]
	return err
}

func (w *FlushWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		_, _ = w.conn.Write(w.buf)
	}
	windowBuff.Put(w.buf)
	w.buf = nil
	return nil
}
