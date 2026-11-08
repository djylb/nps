package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

// HandshakeTimeout bounds how long an accepted transport may take to present
// its first stream or protocol upgrade before it is dropped.
const HandshakeTimeout = 10 * time.Second

// ListenKCP opens the KCP listener of the bridge on addr.
func ListenKCP(addr string) (*kcp.Listener, error) {
	return kcp.ListenWithOptions(addr, nil, 10, 3)
}

// ServeKCP hands every session l accepts to f. It returns once l fails or
// is closed, and closes l.
func ServeKCP(l *kcp.Listener, f func(c net.Conn)) error {
	defer func() { _ = l.Close() }()
	// Without a deadline AcceptKCP fails only once the listener is closed or
	// its socket has failed, and keeps failing after that.
	for {
		c, err := l.AcceptKCP()
		if err != nil {
			return err
		}
		SetUdpSession(c)
		go f(c)
	}
}

// ListenQUIC opens the QUIC listener of the bridge on addr.
func ListenQUIC(addr string, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Listener, error) {
	return quic.ListenAddr(addr, tlsConfig, quicConfig)
}

// ServeQUIC hands the first stream of every connection l accepts to f. It
// returns once l is closed, and closes l.
func ServeQUIC(l *quic.Listener, f func(c net.Conn)) error {
	defer func() { _ = l.Close() }()
	for {
		// Accept fails only once the listener is closed.
		sess, err := l.Accept(context.Background())
		if err != nil {
			return err
		}
		go AcceptQuicStream(sess, f)
	}
}

// AcceptQuicStream hands the first stream of sess to f, or closes sess when
// none comes in time.
func AcceptQuicStream(sess *quic.Conn, f func(c net.Conn)) {
	ctx, cancel := context.WithTimeout(sess.Context(), HandshakeTimeout)
	stream, err := sess.AcceptStream(ctx)
	cancel()
	if err != nil {
		log.Log(context.Background(), slogx.LevelTrace, "quic stream not accepted", "err", err)
		_ = sess.CloseWithError(0, "closed")
		return
	}
	f(NewQuicAutoCloseConn(stream, sess))
}

// Accept serves l until it is closed or fails; netx.Accept retries
// transient errors with a backoff.
// Accept hands every connection l accepts to f until l fails or is closed.
// It returns nil once l is closed and the accept error otherwise.
func Accept(l net.Listener, f func(c net.Conn)) error {
	for {
		c, err := netx.Accept(context.Background(), l)
		if err != nil {
			if netx.IsClosed(err) {
				return nil
			}
			log.Error("accept failed", "listen", l.Addr().String(), "err", err)
			return err
		}
		go f(c)
	}
}

// virtualQueue is how many connections a virtual listener holds before
// Accept takes them.
const virtualQueue = 1024

// NewVirtualListener returns an in-memory listener that reports addr, or
// LocalTCPAddr when addr is nil.
func NewVirtualListener(addr net.Addr) *netx.ChanListener {
	if addr == nil {
		addr = LocalTCPAddr
	}
	return netx.NewChanListener(addr, virtualQueue)
}

// ServeVirtual hands c to l, closing c if l is closed or its queue is full.
func ServeVirtual(l *netx.ChanListener, c net.Conn) {
	if !l.TryDeliver(c) {
		_ = c.Close()
	}
}

// DialVirtual connects to l through an in-memory pipe, with remote, a
// host:port address, as the address of the dialing side. It waits for queue
// space until ctx is done or l is closed.
func DialVirtual(ctx context.Context, l *netx.ChanListener, remote string) (net.Conn, error) {
	addr, err := ParseTCPAddr(remote)
	if err == nil && addr == nil {
		err = errors.New("empty address")
	}
	if err != nil {
		return nil, fmt.Errorf("invalid remote addr %q: %w", remote, err)
	}
	return l.Dial(ctx, addr)
}

// ParseTCPAddr resolves s, a host:port address. An empty s yields a nil
// address and no error.
func ParseTCPAddr(s string) (net.Addr, error) {
	if s == "" {
		return nil, nil
	}
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		return nil, err
	}
	return a, nil
}
