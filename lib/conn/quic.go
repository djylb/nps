package conn

import (
	"context"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

type QuicStreamConn struct {
	stream *quic.Stream
	sess   *quic.Conn
}

func NewQuicStreamConn(stream *quic.Stream, sess *quic.Conn) *QuicStreamConn {
	return &QuicStreamConn{stream: stream, sess: sess}
}

func (q *QuicStreamConn) GetSession() *quic.Conn {
	return q.sess
}

func (q *QuicStreamConn) Read(p []byte) (int, error) {
	return q.stream.Read(p)
}

func (q *QuicStreamConn) Write(p []byte) (int, error) {
	return q.stream.Write(p)
}

func (q *QuicStreamConn) Close() error {
	return q.stream.Close()
}

func (q *QuicStreamConn) LocalAddr() net.Addr {
	return q.sess.LocalAddr()
}

func (q *QuicStreamConn) RemoteAddr() net.Addr {
	return q.sess.RemoteAddr()
}

func (q *QuicStreamConn) SetDeadline(t time.Time) error {
	return q.stream.SetDeadline(t)
}

func (q *QuicStreamConn) SetReadDeadline(t time.Time) error {
	return q.stream.SetReadDeadline(t)
}

func (q *QuicStreamConn) SetWriteDeadline(t time.Time) error {
	return q.stream.SetWriteDeadline(t)
}

type QuicAutoCloseConn struct{ *QuicStreamConn }

func NewQuicAutoCloseConn(stream *quic.Stream, sess *quic.Conn) *QuicAutoCloseConn {
	return &QuicAutoCloseConn{NewQuicStreamConn(stream, sess)}
}

func (q *QuicAutoCloseConn) Close() error {
	_ = q.QuicStreamConn.Close()
	drain := 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	select {
	case <-q.QuicStreamConn.stream.Context().Done():
	case <-ctx.Done():
	}
	return q.sess.CloseWithError(0, "close")
}
