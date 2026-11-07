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
	kind Kind
	conn net.Conn
}

// serveAll accepts from every listener into one channel until they close.
func serveAll(ls map[Kind]net.Listener) chan acceptedConn {
	ch := make(chan acceptedConn, 16)
	for k, l := range ls {
		go acceptInto(k, l, ch)
	}
	return ch
}

func acceptInto(k Kind, l net.Listener, ch chan acceptedConn) {
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
func muxRecv(t *testing.T, port int, payload []byte, ch chan acceptedConn) Kind {
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

func newTestMux(t *testing.T, kinds ...Kind) (*portMux, int, map[Kind]net.Listener, chan acceptedConn) {
	t.Helper()
	port := getAvailablePort(t)
	p, err := newPortMux(port, "admin.test:8080", "bridge.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ls := map[Kind]net.Listener{}
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
	_, port, _, ch := newTestMux(t, KindBridgeTCP, KindBridgeTLS, KindBridgeWS, KindBridgeWSS, KindHTTP, KindHTTPS, KindWeb)
	cases := []struct {
		name    string
		payload []byte
		want    Kind
	}{
		{"client", []byte("TST "), KindBridgeTCP},
		{"manager", get("ADMIN.test:8080"), KindWeb},
		{"ws", get("bridge.test"), KindBridgeWS},
		{"http", get("other.test"), KindHTTP},
		{"tls no sni", clientHello(t, ""), KindBridgeTLS},
		{"tls bridge sni", clientHello(t, "bridge.test"), KindBridgeTLS},
		{"https", clientHello(t, "site.test"), KindHTTPS},
		{"other bytes", []byte("\x00\x01\x02"), KindHTTPS},
	}
	for _, tc := range cases {
		if got := muxRecv(t, port, tc.payload, ch); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPortMuxSingleListenerTakesAll(t *testing.T) {
	_, port, _, ch := newTestMux(t, KindWeb, KindBridgeWSS)
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: 10.0.0.1\r\n\r\n"), ch); got != KindWeb {
		t.Errorf("http: got %d", got)
	}
	if got := muxRecv(t, port, clientHello(t, "x.test"), ch); got != KindBridgeWSS {
		t.Errorf("tls: got %d", got)
	}
}

func TestPortMuxClosedListenerKeepsSlot(t *testing.T) {
	p, port, ls, ch := newTestMux(t, KindHTTP, KindWeb)
	_ = ls[KindHTTP].Close()
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: other.test\r\n\r\n"), ch); got != -1 {
		t.Errorf("got %d, want dropped", got)
	}
	l, err := p.register(KindHTTP)
	if err != nil {
		t.Fatal(err)
	}
	go acceptInto(KindHTTP, l, ch)
	if got := muxRecv(t, port, []byte("GET / HTTP/1.1\r\nHost: other.test\r\n\r\n"), ch); got != KindHTTP {
		t.Errorf("after replace: got %d", got)
	}
}
