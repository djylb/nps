package conn

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/kcp-go/v5"
)

var LocalTCPAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}

type Conn struct {
	Conn       net.Conn
	wBuf       *bytes.Buffer
	mu         sync.Mutex
	closed     uint32
	hookMu     sync.Mutex
	closeHooks []func(*Conn)
}

// NewConn new conn
func NewConn(conn net.Conn) *Conn {
	return &Conn{
		Conn: conn,
	}
}

func (s *Conn) OnClose(fn func(*Conn)) *Conn {
	if fn == nil {
		return s
	}
	s.hookMu.Lock()
	if !s.IsClosed() {
		s.closeHooks = append(s.closeHooks, fn)
		s.hookMu.Unlock()
		return s
	}
	s.hookMu.Unlock()
	// already closed: Close has taken its hooks, so run this one now
	runCloseHook(fn, s)
	return s
}

func runCloseHook(fn func(*Conn), c *Conn) {
	defer func() { _ = recover() }()
	fn(c)
}

// SetAlive set alive
func (s *Conn) SetAlive() {
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Time{})
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Time{})
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Time{})
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Time{})
		}
	}
}

// SetReadDeadlineBySecond set read deadline
func (s *Conn) SetReadDeadlineBySecond(t time.Duration) {
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Now().Add(t * time.Second))
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Now().Add(t * time.Second))
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Now().Add(t * time.Second))
		}
	}
}

func (s *Conn) IsClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *Conn) Close() error {
	if atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		s.hookMu.Lock()
		hooks := s.closeHooks
		s.closeHooks = nil
		s.hookMu.Unlock()
		for _, h := range hooks {
			runCloseHook(h, s)
		}
		s.mu.Lock()
		if s.wBuf != nil {
			s.wBuf.Reset()
		}
		s.mu.Unlock()
		return s.Conn.Close()
	}
	return errors.New("connection already closed")
}

// write
func (s *Conn) Write(b []byte) (n int, err error) {
	if s == nil || s.IsClosed() {
		return 0, errors.New("connection error")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil || s.wBuf.Len() == 0 {
		return s.Conn.Write(b)
	}
	n, err = s.wBuf.Write(b)
	toSend := s.wBuf.Bytes()
	s.wBuf.Reset()

	if _, err := s.Conn.Write(toSend); err != nil {
		return 0, err
	}

	return
}

func (s *Conn) BufferWrite(b []byte) (int, error) {
	if s == nil || s.IsClosed() {
		return 0, errors.New("connection error")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil {
		s.wBuf = new(bytes.Buffer)
	}
	return s.wBuf.Write(b)
}

func (s *Conn) FlushBuf() error {
	if s.IsClosed() {
		return errors.New("connection closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil || s.wBuf.Len() == 0 {
		return nil
	}
	_, err := s.Conn.Write(s.wBuf.Bytes())
	s.wBuf.Reset()
	return err
}

// read
func (s *Conn) Read(b []byte) (n int, err error) {
	if err = s.FlushBuf(); err != nil {
		return 0, err
	}

	return s.Conn.Read(b)
}

func (s *Conn) LocalAddr() net.Addr {
	return s.Conn.LocalAddr()
}

func (s *Conn) RemoteAddr() net.Addr {
	return s.Conn.RemoteAddr()
}

func (s *Conn) SetDeadline(t time.Time) error {
	return s.Conn.SetDeadline(t)
}

func (s *Conn) SetWriteDeadline(t time.Time) error {
	return s.Conn.SetWriteDeadline(t)
}

func (s *Conn) SetReadDeadline(t time.Time) error {
	return s.Conn.SetReadDeadline(t)
}
