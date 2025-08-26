package conn

import (
	"io"
	"net"
	"time"
)

type wrappedConn struct {
	rwc    io.ReadWriteCloser
	parent net.Conn
}

func WrapConn(rwc io.ReadWriteCloser, parent net.Conn) net.Conn {
	return &wrappedConn{rwc: rwc, parent: parent}
}

func (w *wrappedConn) Read(b []byte) (int, error) {
	return w.rwc.Read(b)
}

func (w *wrappedConn) Write(b []byte) (int, error) {
	return w.rwc.Write(b)
}

func (w *wrappedConn) Close() error {
	_ = w.rwc.Close()
	return w.parent.Close()
}

func (w *wrappedConn) LocalAddr() net.Addr {
	return w.parent.LocalAddr()
}

func (w *wrappedConn) RemoteAddr() net.Addr {
	return w.parent.RemoteAddr()
}

func (w *wrappedConn) SetDeadline(t time.Time) error {
	return w.parent.SetDeadline(t)
}

func (w *wrappedConn) SetReadDeadline(t time.Time) error {
	return w.parent.SetReadDeadline(t)
}

func (w *wrappedConn) SetWriteDeadline(t time.Time) error {
	return w.parent.SetWriteDeadline(t)
}
