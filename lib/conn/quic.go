package conn

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
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

type QuicAutoCloseConn struct {
	*QuicStreamConn
	closeOnce sync.Once
}

func NewQuicAutoCloseConn(stream *quic.Stream, sess *quic.Conn) *QuicAutoCloseConn {
	return &QuicAutoCloseConn{QuicStreamConn: NewQuicStreamConn(stream, sess)}
}

// quicCloseDrain is how long a closed QuicAutoCloseConn keeps its connection
// so the data and FIN already written can reach the peer.
const quicCloseDrain = 300 * time.Millisecond

// Close closes the stream and, unless the peer closes the connection first,
// the connection after quicCloseDrain. Closing the connection right away
// would discard stream data the peer has not acknowledged yet.
func (q *QuicAutoCloseConn) Close() error {
	q.closeOnce.Do(func() {
		_ = q.QuicStreamConn.Close()
		go func() {
			t := time.NewTimer(quicCloseDrain)
			defer t.Stop()
			select {
			case <-q.sess.Context().Done():
			case <-t.C:
			}
			_ = q.sess.CloseWithError(0, "close")
		}()
	})
	return nil
}

// QuicCarrier makes a QUIC connection a carrier of streams, so that nothing
// but this file has to know about QUIC to use it.
type QuicCarrier struct {
	sess *quic.Conn
}

// NewQuicCarrier returns the carrier of sess.
func NewQuicCarrier(sess *quic.Conn) *QuicCarrier { return &QuicCarrier{sess: sess} }

// OpenStream opens a stream on the connection.
func (q *QuicCarrier) OpenStream(ctx context.Context) (net.Conn, error) {
	stream, err := q.sess.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return NewQuicStreamConn(stream, q.sess), nil
}

// AcceptStream returns the next stream the peer opened.
func (q *QuicCarrier) AcceptStream(ctx context.Context) (net.Conn, error) {
	stream, err := q.sess.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return NewQuicStreamConn(stream, q.sess), nil
}

// Close closes the connection and its streams.
func (q *QuicCarrier) Close() error { return q.sess.CloseWithError(0, "close") }

// Closed reports whether the connection has closed.
func (q *QuicCarrier) Closed() bool { return q.sess.Context().Err() != nil }

// Reason says why the connection closed.
func (q *QuicCarrier) Reason() string {
	if err := context.Cause(q.sess.Context()); err != nil {
		return err.Error()
	}
	return ""
}

// CarrierOf returns the carrier of a connection whose first stream is c, if
// c is a QUIC stream.
func CarrierOf(c net.Conn) (*QuicCarrier, bool) {
	qc, ok := c.(*QuicAutoCloseConn)
	if !ok {
		return nil, false
	}
	return NewQuicCarrier(qc.GetSession()), true
}

// SignalStream makes a stream opened for a signal connection end its whole
// connection when it closes, as a signal connection's end means the end of the
// connection; other streams are returned as they are.
func SignalStream(c net.Conn) net.Conn {
	if qs, ok := c.(*QuicStreamConn); ok {
		return NewQuicAutoCloseConn(qs.stream, qs.sess)
	}
	return c
}

func DialQuicWithLocalIP(ctx context.Context, server string, tlsCfg *tls.Config, quicCfg *quic.Config, localIP string) (*quic.Conn, error) {
	bindAddr := common.BuildUDPBindAddr(localIP)
	if bindAddr == nil {
		return quic.DialAddr(ctx, server, tlsCfg, quicCfg)
	}
	rAddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	packetConn, err := net.ListenUDP("udp", bindAddr)
	if err != nil {
		return nil, err
	}
	sess, err := quic.Dial(ctx, packetConn, rAddr, tlsCfg, quicCfg)
	if err != nil {
		_ = packetConn.Close()
		return nil, err
	}
	// quic.Dial does not take ownership of a caller-supplied PacketConn.
	go func() {
		<-sess.Context().Done()
		_ = packetConn.Close()
	}()
	return sess, nil
}
