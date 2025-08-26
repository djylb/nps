package conn

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type WsConn struct {
	*websocket.Conn
	readBuf []byte
}

func NewWsConn(ws *websocket.Conn) *WsConn {
	return &WsConn{Conn: ws, readBuf: make([]byte, 0)}
}

func (c *WsConn) Read(p []byte) (int, error) {
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}
	mt, r, err := c.NextReader()
	if err != nil {
		return 0, err
	}
	if mt == websocket.CloseMessage {
		return 0, io.EOF
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	n := copy(p, data)
	if n < len(data) {
		c.readBuf = append(c.readBuf, data[n:]...)
	}
	return n, nil
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

func (c *WsConn) Close() error         { return c.Conn.Close() }
func (c *WsConn) LocalAddr() net.Addr  { return c.Conn.NetConn().LocalAddr() }
func (c *WsConn) RemoteAddr() net.Addr { return c.Conn.NetConn().RemoteAddr() }
func (c *WsConn) SetDeadline(t time.Time) error {
	_ = c.Conn.SetReadDeadline(t)
	return c.Conn.SetWriteDeadline(t)
}
func (c *WsConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *WsConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }

type httpListener struct {
	acceptCh chan net.Conn
	closeCh  chan struct{}
	addr     net.Addr
}

func NewWSListener(base net.Listener, path string) net.Listener {
	ch := make(chan net.Conn, 16)
	hl := &httpListener{acceptCh: ch, closeCh: make(chan struct{}), addr: base.Addr()}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hl.closeCh:
			return
		default:
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ch <- NewWsConn(ws)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(base)
	go func() {
		<-hl.closeCh
		_ = srv.Close()
	}()
	return hl
}

func NewWSSListener(base net.Listener, path string, cert tls.Certificate) net.Listener {
	ch := make(chan net.Conn, 16)
	hl := &httpListener{acceptCh: ch, closeCh: make(chan struct{}), addr: base.Addr()}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hl.closeCh:
			return
		default:
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ch <- NewWsConn(ws)
	})
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	srv := &http.Server{Handler: mux, TLSConfig: tlsConfig}
	go srv.Serve(tls.NewListener(base, tlsConfig))
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
	close(hl.closeCh)
	return nil
}

func (hl *httpListener) Addr() net.Addr {
	return hl.addr
}

func DialWS(rawConn net.Conn, urlStr string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rawConn, nil
		},
	}
	return dialer.DialContext(ctx, urlStr, nil)
}

func DialWSS(rawConn net.Conn, urlStr string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rawConn, nil
		},
	}
	return dialer.DialContext(ctx, urlStr, nil)
}
