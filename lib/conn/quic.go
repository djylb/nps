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
