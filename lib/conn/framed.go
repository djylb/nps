package conn

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const MaxFramePayload = 65535

var ErrFrameTooLarge = errors.New("framed: frame size exceeds MaxFramePayload")

type FramedConn struct {
	net.Conn
	rmu sync.Mutex
	wmu sync.Mutex
}

func WrapFramed(c net.Conn) *FramedConn { return &FramedConn{Conn: c} }

func (fc *FramedConn) Read(p []byte) (int, error) {
	fc.rmu.Lock()
	defer fc.rmu.Unlock()

	var hdr [2]byte
	if _, err := io.ReadFull(fc.Conn, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))

	if n == 0 {
		return 0, nil
	}
	if n > MaxFramePayload {
		return 0, ErrFrameTooLarge
	}

	if len(p) >= n {
		_, err := io.ReadFull(fc.Conn, p[:n])
		return n, err
	}

	read := 0
	if len(p) > 0 {
		if _, err := io.ReadFull(fc.Conn, p[:]); err != nil {
			return 0, err
		}
		read = len(p)
	}
	remain := n - read
	if remain > 0 {
		if _, err := io.CopyN(io.Discard, fc.Conn, int64(remain)); err != nil {
			return read, err
		}
	}
	return read, nil
}

func (fc *FramedConn) Write(p []byte) (int, error) {
	fc.wmu.Lock()
	defer fc.wmu.Unlock()

	send := len(p)
	if send > MaxFramePayload {
		send = MaxFramePayload
	}

	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(send))
	for off := 0; off < len(hdr); {
		n, err := fc.Conn.Write(hdr[off:])
		if n > 0 {
			off += n
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.ErrShortWrite
		}
	}

	for off := 0; off < send; {
		n, err := fc.Conn.Write(p[off:send])
		if n > 0 {
			off += n
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.ErrShortWrite
		}
	}

	return len(p), nil
}

func (fc *FramedConn) SetDeadline(t time.Time) error      { return fc.Conn.SetDeadline(t) }
func (fc *FramedConn) SetReadDeadline(t time.Time) error  { return fc.Conn.SetReadDeadline(t) }
func (fc *FramedConn) SetWriteDeadline(t time.Time) error { return fc.Conn.SetWriteDeadline(t) }
func (fc *FramedConn) LocalAddr() net.Addr                { return fc.Conn.LocalAddr() }
func (fc *FramedConn) RemoteAddr() net.Addr               { return fc.Conn.RemoteAddr() }
func (fc *FramedConn) Close() error                       { return fc.Conn.Close() }
