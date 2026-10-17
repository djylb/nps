package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// localDialBridge serves local-proxy links by dialing the target directly.
type localDialBridge struct {
	links chan *conn.Link
}

func (b *localDialBridge) SendLinkInfo(_ int, link *conn.Link, _ *file.Tunnel) (net.Conn, error) {
	b.links <- link
	return net.DialTimeout(link.ConnType, link.Host, 2*time.Second)
}

func (b *localDialBridge) IsServer() bool { return true }

func (b *localDialBridge) CliProcess(*conn.Conn, string) {}

func tcpConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	dialer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = dialer.Close(); _ = server.Close() })
	return dialer, server
}

func TestSecretLocalProxySendsProxyHeaderAndPreReadToBackend(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()

	type received struct {
		header string
		body   []byte
		err    error
	}
	got := make(chan received, 1)
	go func() {
		c, err := backend.Accept()
		if err != nil {
			got <- received{err: err}
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(c)
		header, err := r.ReadString('\n')
		if err != nil {
			got <- received{err: err}
			return
		}
		body := make([]byte, len("GET / HTTP/1.0\r\n\r\n"))
		_, err = io.ReadFull(r, body)
		got <- received{header: header, body: body, err: err}
		_, _ = c.Write([]byte("pong"))
	}()

	task := &file.Tunnel{
		Mode: "secret",
		// A negative client id is served by the server itself, so the
		// connection is proxied locally even without link information.
		Client: &file.Client{
			Id:   -1,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: backend.Addr().String(), LocalProxy: true, ProxyProtocol: 1},
	}
	bridge := &localDialBridge{links: make(chan *conn.Link, 1)}
	s := NewSecretServer(bridge, task, true, false, false)

	visitor, src := tcpConnPair(t)
	done := make(chan struct{})
	go func() {
		_ = s.HandleSecret(src)
		close(done)
	}()

	// A visitor that does not send link information: its first bytes are
	// consumed while probing and must still reach the backend.
	request := []byte("GET / HTTP/1.0\r\n\r\n")
	if _, err := visitor.Write(request); err != nil {
		t.Fatal(err)
	}

	if link := <-bridge.links; !link.LocalProxy {
		t.Fatalf("link must be served locally")
	}
	r := <-got
	if r.err != nil {
		t.Fatalf("backend read: %v (header %q)", r.err, r.header)
	}
	if !strings.HasPrefix(r.header, "PROXY TCP4 "+visitor.LocalAddr().(*net.TCPAddr).IP.String()+" ") {
		t.Fatalf("backend header = %q", r.header)
	}
	if !bytes.Equal(r.body, request) {
		t.Fatalf("backend body = %q", r.body)
	}

	_ = visitor.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, 4)
	if _, err := io.ReadFull(visitor, reply); err != nil {
		t.Fatalf("visitor read: %v", err)
	}
	if string(reply) != "pong" {
		t.Fatalf("visitor received %q before the backend reply", reply)
	}
	_ = visitor.Close()
	<-done
}

func TestSecretLocalProxyRelaysUDPDatagrams(t *testing.T) {
	backend, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 2048)
		n, addr, err := backend.ReadFrom(buf)
		if err != nil {
			close(got)
			return
		}
		got <- append([]byte(nil), buf[:n]...)
		_, _ = backend.WriteTo([]byte("pong"), addr)
	}()

	task := &file.Tunnel{
		Mode:       "secret",
		TargetType: "all",
		Client: &file.Client{
			Id:   -1,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: backend.LocalAddr().String(), ProxyProtocol: 2},
	}
	bridge := &localDialBridge{links: make(chan *conn.Link, 1)}
	s := NewSecretServer(bridge, task, true, false, false)

	visitor, src := tcpConnPair(t)
	done := make(chan struct{})
	go func() {
		_ = s.HandleSecret(src)
		close(done)
	}()

	lk := conn.NewLink("udp", "", false, false, visitor.LocalAddr().String(), false)
	if _, err := conn.NewConn(visitor).SendInfo(lk, ""); err != nil {
		t.Fatal(err)
	}
	if link := <-bridge.links; link.ConnType != "udp" || !link.LocalProxy {
		t.Fatalf("unexpected link %+v", link)
	}
	framed := netx.NewFramedConn(visitor, netx.WithFramedDatagrams())
	if _, err := framed.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-got:
		if string(d) != "ping" {
			t.Fatalf("backend datagram = %q", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("backend received nothing")
	}
	_ = visitor.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, 64)
	n, err := framed.Read(reply)
	if err != nil || string(reply[:n]) != "pong" {
		t.Fatalf("visitor reply %q, %v", reply[:n], err)
	}
	_ = visitor.Close()
	<-done
}
