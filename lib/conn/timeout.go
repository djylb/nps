package conn

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
)

type TimeoutConn struct {
	net.Conn
	idleTimeout time.Duration
	mu          sync.Mutex
	lastSet     time.Time
}

func NewTimeoutConn(c net.Conn, idle time.Duration) net.Conn {
	return &TimeoutConn{Conn: c, idleTimeout: idle}
}

func (c *TimeoutConn) Read(b []byte) (int, error) {
	c.refreshDeadline()
	return c.Conn.Read(b)
}

func (c *TimeoutConn) Write(b []byte) (int, error) {
	c.refreshDeadline()
	return c.Conn.Write(b)
}

func (c *TimeoutConn) refreshDeadline() {
	deadline := time.Now().Add(c.idleTimeout)
	c.mu.Lock()
	if !c.lastSet.IsZero() && !deadline.After(c.lastSet) {
		deadline = c.lastSet.Add(time.Nanosecond)
	}
	c.lastSet = deadline
	c.mu.Unlock()
	_ = c.SetDeadline(deadline)
}

func NewTimeoutTLSConn(raw net.Conn, cfg *tls.Config, idle, handshakeTimeout time.Duration) (net.Conn, error) {
	if err := raw.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		_ = raw.Close()
		return nil, err
	}
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.Handshake(); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return NewTimeoutConn(tlsConn, idle), nil
}
