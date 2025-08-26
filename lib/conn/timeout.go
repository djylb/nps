package conn

import (
	"crypto/tls"
	"net"
	"time"
)

type TimeoutConn struct {
	net.Conn
	idleTimeout time.Duration
}

func NewTimeoutConn(c net.Conn, idle time.Duration) net.Conn {
	return &TimeoutConn{Conn: c, idleTimeout: idle}
}

func (c *TimeoutConn) Read(b []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(c.idleTimeout))
	return c.Conn.Read(b)
}

func (c *TimeoutConn) Write(b []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(c.idleTimeout))
	return c.Conn.Write(b)
}

func NewTimeoutTLSConn(raw net.Conn, cfg *tls.Config, idle, handshakeTimeout time.Duration) (net.Conn, error) {
	_ = raw.SetDeadline(time.Now().Add(handshakeTimeout))
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.Handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return NewTimeoutConn(tlsConn, idle), nil
}
