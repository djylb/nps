package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/logs"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

// HandshakeTimeout bounds how long an accepted transport may take to present
// its first stream or protocol upgrade before it is dropped.
const HandshakeTimeout = 10 * time.Second

const maxAcceptBackoff = time.Second

func NewTcpListenerAndProcess(addr string, f func(c net.Conn), listener *net.Listener) error {
	var err error
	*listener, err = net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	Accept(*listener, f)
	return nil
}

func NewKcpListenerAndProcess(addr string, f func(c net.Conn)) error {
	kcpListener, err := kcp.ListenWithOptions(addr, nil, 10, 3)
	if err != nil {
		logs.Error("KCP listen error: %v", err)
		return err
	}
	for {
		c, err := kcpListener.AcceptKCP()
		if err != nil {
			//if errors.Is(err, net.ErrClosed) {
			//	logs.Info("KCP listener closed intentionally.")
			//	break
			//}
			logs.Trace("KCP accept session error: %v", err)
			continue
		}
		SetUdpSession(c)
		go f(c)
	}
	//return nil
}

func NewQuicListenerAndProcess(addr string, tlsConfig *tls.Config, quicConfig *quic.Config, f func(c net.Conn)) error {
	listener, err := quic.ListenAddr(addr, tlsConfig, quicConfig)
	if err != nil {
		logs.Error("QUIC listen error: %v", err)
		return err
	}
	for {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			logs.Warn("QUIC accept session error: %v", err)
			continue
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

type OneConnListener struct {
	conn      net.Conn
	accepted  bool
	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

func NewOneConnListener(c net.Conn) *OneConnListener {
	return &OneConnListener{
		conn: c,
		done: make(chan struct{}),
	}
}

func (l *OneConnListener) Accept() (net.Conn, error) {
	//logs.Trace("OneConnListener Accept")
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, io.EOF
}

func (l *OneConnListener) Close() error {
	err := l.conn.Close()
	l.closeOnce.Do(func() {
		close(l.done)
		//logs.Trace("OneConnListener Close")
	})
	return err
}

func (l *OneConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

type VirtualListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	addr      net.Addr
	closeOnce sync.Once
}

func NewVirtualListener(addr net.Addr) *VirtualListener {
	if addr == nil {
		addr = LocalTCPAddr
	}
	return &VirtualListener{
		conns:  make(chan net.Conn, 1024),
		closed: make(chan struct{}),
		addr:   addr,
	}
}

func (l *VirtualListener) SetAddr(addr net.Addr) {
	if addr != nil {
		l.addr = addr
	}
}

func (l *VirtualListener) Addr() net.Addr {
	return l.addr
}

func (l *VirtualListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *VirtualListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
		for {
			select {
			case c := <-l.conns:
				if c != nil {
					_ = c.Close()
				}
			default:
				return
			}
		}
	})
	return nil
}

func (l *VirtualListener) ServeVirtual(c net.Conn) {
	select {
	case <-l.closed:
		_ = c.Close()
	case l.conns <- c:
	default:
		_ = c.Close()
	}
}

func (l *VirtualListener) DialVirtual(rAddr string) (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	a, b := net.Pipe()
	remoteAddr, err := parseTCPAddrMaybe(rAddr)
	if err != nil || remoteAddr == nil {
		_ = a.Close()
		_ = b.Close()
		return nil, fmt.Errorf("invalid remote addr %q: %w", rAddr, err)
	}
	c := NewAddrOverrideFromAddr(b, remoteAddr, l.addr)
	l.ServeVirtual(c)
	return a, nil
}
