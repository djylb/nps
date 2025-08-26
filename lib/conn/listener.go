package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/logs"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

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
	kcpListener, err := kcp.ListenWithOptions(addr, nil, 150, 3)
	if err != nil {
		logs.Error("KCP listen error: %v", err)
		return err
	}
	for {
		c, err := kcpListener.AcceptKCP()
		SetUdpSession(c)
		if err != nil {
			logs.Trace("KCP accept session error: %v", err)
			continue
		}
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
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			logs.Trace("QUIC accept stream error: %v", err)
			continue
		}
		conn := NewQuicAutoCloseConn(stream, sess)
		go f(conn)
	}
}

func Accept(l net.Listener, f func(c net.Conn)) {
	for {
		c, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			if strings.Contains(err.Error(), "the mux has closed") {
				break
			}
			logs.Warn("%v", err)
			continue
		}
		if c == nil {
			logs.Warn("nil connection")
			break
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
	conns  chan net.Conn
	closed chan struct{}
	addr   net.Addr
}

func NewVirtualListener(addr net.Addr) *VirtualListener {
	return &VirtualListener{
		conns:  make(chan net.Conn, 1024),
		closed: make(chan struct{}),
		addr:   addr,
	}
}

func (l *VirtualListener) Addr() net.Addr {
	return l.addr
}

func (l *VirtualListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}

func (l *VirtualListener) Close() error {
	select {
	case <-l.closed:
		return nil
	default:
		close(l.closed)
	}
	for {
		select {
		case c := <-l.conns:
			_ = c.Close()
		default:
			return nil
		}
	}
}

func (l *VirtualListener) Deliver(c net.Conn) {
	select {
	case <-l.closed:
		_ = c.Close()
	case l.conns <- c:
	}
}
