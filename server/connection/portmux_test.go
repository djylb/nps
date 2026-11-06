package connection

import (
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

type acceptedConn struct {
	kind muxKind
	conn net.Conn
}

// serveAll accepts from every listener into one channel until they close.
func serveAll(ls map[muxKind]net.Listener) chan acceptedConn {
	ch := make(chan acceptedConn, 16)
	for k, l := range ls {
		go acceptInto(k, l, ch)
	}
	return ch
}

func acceptInto(k muxKind, l net.Listener, ch chan acceptedConn) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		ch <- acceptedConn{k, c}
	}
}

// muxRecv returns which listener receives a connection that sends payload,
// or -1 when none does within a short time.
func muxRecv(t *testing.T, port int, payload []byte, ch chan acceptedConn) muxKind {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-ch:
		defer func() { _ = a.conn.Close() }()
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(a.conn, buf); err != nil || string(buf) != string(payload) {
			t.Errorf("replayed bytes differ: %v", err)
		}
		return a.kind
	case <-time.After(500 * time.Millisecond):
		return -1
	}
}

func clientHello(t *testing.T, sni string) []byte {
	t.Helper()
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() {
		_ = tls.Client(a, &tls.Config{ServerName: sni, InsecureSkipVerify: true}).Handshake()
		_ = a.Close()
	}()
	buf := make([]byte, 4096)
	n, _ := b.Read(buf)
	return buf[:n]
}

func newTestMux(t *testing.T, kinds ...muxKind) (*portMux, int, map[muxKind]net.Listener, chan acceptedConn) {
	t.Helper()
	port := getAvailablePort(t)
	p, err := newPortMux(port, "admin.test:8080", "bridge.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ls := map[muxKind]net.Listener{}
	for _, k := range kinds {
		l, err := p.register(k)
		if err != nil {
			t.Fatal(err)
		}
		ls[k] = l
	}
	return p, port, ls, serveAll(ls)
}

func TestPortMuxRouting(t *testing.T) {
	get := func(host string) []byte {
		return []byte("GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n")
	}
	_, port, _, ch := newTestMux(t, kindClient, kindClientTls, kindClientWs, kindClientWss, kindHttp, kindHttps, kindManager)
	cases := []struct {
		name    string
		payload []byte
		want    muxKind
	}{
		{"client", []byte("TST "), kindClient},
		{"manager", get("ADMIN.test:8080"), kindManager},
		{"ws", get("bridge.test"), kindClientWs},
		{"http", get("other.test"), kindHttp},
		{"tls no sni", clientHello(t, ""), kindClientTls},
		{"tls bridge sni", clientHello(t, "bridge.test"), kindClientTls},
		{"https", clientHello(t, "site.test"), kindHttps},
		{"other bytes", []byte("\x00\x01\x02"), kindHttps},
	}
	for _, tc := range cases {
		if got := muxRecv(t, port, tc.payload, ch); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPortMuxSingleListenerTakesAll(t *testing.T) {
	_, port, _, ch := newTestMux(t, kindManager, kindClientWss)
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: 10.0.0.1\r\n\r\n"), ch); got != kindManager {
		t.Errorf("http: got %d", got)
	}
	if got := muxRecv(t, port, clientHello(t, "x.test"), ch); got != kindClientWss {
		t.Errorf("tls: got %d", got)
	}
}

func TestPortMuxClosedListenerKeepsSlot(t *testing.T) {
	p, port, ls, ch := newTestMux(t, kindHttp, kindManager)
	_ = ls[kindHttp].Close()
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: other.test\r\n\r\n"), ch); got != -1 {
		t.Errorf("got %d, want dropped", got)
	}
	l, err := p.register(kindHttp)
	if err != nil {
		t.Fatal(err)
	}
	go acceptInto(kindHttp, l, ch)
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: other.test\r\n\r\n"), ch); got != kindHttp {
		t.Errorf("after replace: got %d", got)
	}
}
