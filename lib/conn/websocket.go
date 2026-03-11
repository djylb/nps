package conn

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/gorilla/websocket"
)

type WsConn struct {
	*websocket.Conn
	RealIP    string
	readFrame io.Reader
}

func NewWsConn(ws *websocket.Conn) *WsConn {
	return &WsConn{Conn: ws}
}

func (c *WsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if c.readFrame == nil {
			mt, r, err := c.NextReader()
			if err != nil {
				return 0, err
			}
			if mt == websocket.CloseMessage {
				return 0, io.EOF
			}
			c.readFrame = r
		}

		n, err := c.readFrame.Read(p)
		switch {
		case n > 0 && err == io.EOF:
			c.readFrame = nil
			return n, nil
		case n > 0:
			return n, err
		case err == io.EOF:
			c.readFrame = nil
			continue
		default:
			return 0, err
		}
	}
}

func (c *WsConn) Write(p []byte) (int, error) {
	w, err := c.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(p)
	if err != nil {
		return n, err
	}
	return n, w.Close()
}

func (c *WsConn) Close() error        { return c.Conn.Close() }
func (c *WsConn) LocalAddr() net.Addr { return c.Conn.NetConn().LocalAddr() }
func (c *WsConn) RemoteAddr() net.Addr {
	if c.RealIP != "" {
		if ip := net.ParseIP(c.RealIP); ip != nil {
			return &net.TCPAddr{
				IP:   ip,
				Port: 0,
			}
		}
	}
	return c.Conn.NetConn().RemoteAddr()
}
func (c *WsConn) SetDeadline(t time.Time) error {
	_ = c.Conn.SetReadDeadline(t)
	return c.Conn.SetWriteDeadline(t)
}
func (c *WsConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *WsConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }

type httpListener struct {
	acceptCh  chan net.Conn
	closeCh   chan struct{}
	addr      net.Addr
	closeOnce sync.Once
}

func NewWSListener(base net.Listener, path, trustedIps, realIpHeader string) net.Listener {
	ch := make(chan net.Conn, 16)
	hl := &httpListener{acceptCh: ch, closeCh: make(chan struct{}), addr: base.Addr()}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	if path == "" {
		path = "/"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hl.closeCh:
			return
		default:
		}
		realIP := GetRealIP(r, realIpHeader)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := NewWsConn(ws)
		if common.IsTrustedProxy(trustedIps, r.RemoteAddr) {
			c.RealIP = realIP
		}
		select {
		case ch <- c:
		case <-hl.closeCh:
			_ = c.Close()
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(base) }()
	go func() {
		<-hl.closeCh
		_ = srv.Close()
	}()
	return hl
}

func NewWSSListener(base net.Listener, path string, cert tls.Certificate, trustedIps, realIpHeader string) net.Listener {
	ch := make(chan net.Conn, 16)
	hl := &httpListener{acceptCh: ch, closeCh: make(chan struct{}), addr: base.Addr()}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	if path == "" {
		path = "/"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hl.closeCh:
			return
		default:
		}
		realIP := GetRealIP(r, realIpHeader)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := NewWsConn(ws)
		if common.IsTrustedProxy(trustedIps, r.RemoteAddr) {
			c.RealIP = realIP
		}
		select {
		case ch <- c:
		case <-hl.closeCh:
			_ = c.Close()
		}
	})
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	srv := &http.Server{Handler: mux, TLSConfig: tlsConfig}
	go func() { _ = srv.Serve(tls.NewListener(base, tlsConfig)) }()
	go func() {
		<-hl.closeCh
		_ = srv.Close()
	}()
	return hl
}

func (hl *httpListener) Accept() (net.Conn, error) {
	select {
	case c := <-hl.acceptCh:
		return c, nil
	case <-hl.closeCh:
		return nil, io.EOF
	}
}

func (hl *httpListener) Close() error {
	hl.closeOnce.Do(func() {
		close(hl.closeCh)
	})
	return nil
}

func (hl *httpListener) Addr() net.Addr {
	return hl.addr
}

func DialWS(rawConn net.Conn, urlStr, host string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	h := http.Header{}
	if host != "" {
		h.Set("Host", host)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rawConn, nil
		},
	}
	return dialer.DialContext(ctx, urlStr, h)
}

func DialWSS(rawConn net.Conn, urlStr, host, sni string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	h := http.Header{}
	if host != "" {
		h.Set("Host", host)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
		},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rawConn, nil
		},
	}
	return dialer.DialContext(ctx, urlStr, h)
}
