package conn

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

type TlsConn struct {
	*tls.Conn
	rawConn net.Conn
}

func NewTlsConn(rawConn net.Conn, timeout time.Duration, tlsConfig *tls.Config) (*TlsConn, error) {
	if rawConn == nil {
		return nil, fmt.Errorf("rawConn cannot be nil")
	}

	err := rawConn.SetDeadline(time.Now().Add(timeout))
	if err != nil {
		return nil, fmt.Errorf("failed to set deadline for rawConn: %w", err)
	}

	tlsConn := tls.Client(rawConn, tlsConfig)

	if err := tlsConn.Handshake(); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	return &TlsConn{
		Conn:    tlsConn,
		rawConn: rawConn,
	}, nil
}

func (c *TlsConn) GetRawConn() net.Conn {
	return c.rawConn
}

func (c *TlsConn) Close() error {
	if c.Conn != nil {
		if err := c.Conn.Close(); err != nil {
			return fmt.Errorf("failed to close tlsConn: %w", err)
		}
	}

	if c.rawConn != nil {
		if err := c.rawConn.Close(); err != nil {
			return fmt.Errorf("failed to close rawConn: %w", err)
		}
	}

	return nil
}

func (c *TlsConn) Read(b []byte) (n int, err error) {
	return c.Conn.Read(b)
}

func (c *TlsConn) Write(b []byte) (n int, err error) {
	return c.Conn.Write(b)
}

func (c *TlsConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetDeadline(t); err != nil {
		return err
	}
	if err := c.rawConn.SetDeadline(t); err != nil {
		return err
	}
	return nil
}

func (c *TlsConn) SetReadDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return err
	}
	if err := c.rawConn.SetReadDeadline(t); err != nil {
		return err
	}
	return nil
}

func (c *TlsConn) SetWriteDeadline(t time.Time) error {
	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return err
	}
	if err := c.rawConn.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

func (c *TlsConn) LocalAddr() net.Addr {
	return c.rawConn.LocalAddr()
}

func (c *TlsConn) RemoteAddr() net.Addr {
	return c.Conn.RemoteAddr()
}
