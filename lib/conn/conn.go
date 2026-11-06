package conn

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/kcp-go/v5"
)

var LocalTCPAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}

type Conn struct {
	Conn       net.Conn
	rbs        [][]byte
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

func (s *Conn) SetRb(rbs ...[]byte) *Conn {
	for _, rb := range rbs {
		if len(rb) > 0 {
			s.rbs = append(s.rbs, rb)
		}
	}
	return s
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

// GetHost reads one HTTP proxy request header from the connection and resolves
// the target address. head holds the raw header bytes and rest holds everything
// read past the header (request body, pipelined requests or early tunnel data)
// followed by any data still queued on the connection, so nothing stays queued
// on return. On error head holds every byte consumed so far.
func (s *Conn) GetHost() (address string, r *http.Request, head, rest []byte, err error) {
	var raw bytes.Buffer
	br := bufio.NewReader(io.TeeReader(io.LimitReader(s, http.DefaultMaxHeaderBytes), &raw))
	r, err = http.ReadRequest(br)
	if err != nil {
		head = raw.Bytes()
		return
	}
	b := raw.Bytes()
	n := len(b) - br.Buffered()
	head = b[:n:n]
	if len(b) > n {
		rest = b[n:]
	}
	for _, rb := range s.rbs {
		rest = append(rest, rb...)
	}
	s.rbs = nil
	address = requestTargetAddress(r)
	return
}

// requestTargetAddress returns host:port for a proxy request, adding the
// scheme's default port when the request names none.
func requestTargetAddress(r *http.Request) string {
	if _, _, err := net.SplitHostPort(r.Host); err == nil {
		return r.Host
	}
	port := "80"
	if r.Method == http.MethodConnect || strings.EqualFold(r.URL.Scheme, "https") {
		port = "443"
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(r.Host, "["), "]"), port)
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
		// rbs is left alone: a Read running concurrently, as on any conn
		// closed by another goroutine, may still be looking at it.
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

	for len(s.rbs) > 0 {
		cur := s.rbs[0]
		if len(cur) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			continue
		}
		n = copy(b, cur)
		s.rbs[0] = cur[n:]
		if len(s.rbs[0]) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			if len(s.rbs) == 0 {
				s.rbs = nil
			}
		}
		return n, nil
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
