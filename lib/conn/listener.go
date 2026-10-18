package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/logs"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

// HandshakeTimeout bounds how long an accepted transport may take to present
// its first stream or protocol upgrade before it is dropped.
const HandshakeTimeout = 10 * time.Second

const maxAcceptBackoff = time.Second

func NewKcpListenerAndProcess(addr string, f func(c net.Conn)) error {
	kcpListener, err := kcp.ListenWithOptions(addr, nil, 10, 3)
	if err != nil {
		logs.Error("KCP listen error: %v", err)
		return err
	}
	// Without a deadline AcceptKCP fails only once the listener is closed or
	// its socket has failed, and keeps failing after that.
	for {
		c, err := kcpListener.AcceptKCP()
		if err != nil {
			_ = kcpListener.Close()
			return err
		}
		SetUdpSession(c)
		go f(c)
	}
}

func NewQuicListenerAndProcess(addr string, tlsConfig *tls.Config, quicConfig *quic.Config, f func(c net.Conn)) error {
	listener, err := quic.ListenAddr(addr, tlsConfig, quicConfig)
	if err != nil {
		logs.Error("QUIC listen error: %v", err)
		return err
	}
	for {
		// Accept fails only once the listener is closed.
		sess, err := listener.Accept(context.Background())
		if err != nil {
			_ = listener.Close()
			return err
		}
		go acceptQuicStream(sess, f)
	}
}

func acceptQuicStream(sess *quic.Conn, f func(c net.Conn)) {
	ctx, cancel := context.WithTimeout(sess.Context(), HandshakeTimeout)
	stream, err := sess.AcceptStream(ctx)
	cancel()
	if err != nil {
		logs.Trace("QUIC accept stream error: %v", err)
		_ = sess.CloseWithError(0, "closed")
		return
	}
	f(NewQuicAutoCloseConn(stream, sess))
}

// Accept serves l until it is closed, backing off on transient errors.
func Accept(l net.Listener, f func(c net.Conn)) {
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if delay == 0 {
				delay = 5 * time.Millisecond
			} else {
				delay = min(delay*2, maxAcceptBackoff)
			}
			logs.Warn("accept error: %v; retrying in %v", err, delay)
			time.Sleep(delay)
			continue
		}
		delay = 0
		if c == nil {
			logs.Warn("nil connection")
			return
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
