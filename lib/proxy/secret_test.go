package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/acl"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
)

// localDialBridge serves local-proxy links by dialing the target directly.
type localDialBridge struct {
	links chan *conn.Link
}

func (b *localDialBridge) Session(int) conn.ClientSession { return b }

func (b *localDialBridge) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	b.links <- link
	return net.DialTimeout(link.ConnType, link.Host, 2*time.Second)
}

func (b *localDialBridge) IsServer() bool { return true }

func (b *localDialBridge) GlobalSource() *acl.Source { return nil }

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

func (*localDialBridge) LinkCert() *tls.Certificate { return &tls.Certificate{} }

func TestSecretLocalProxySendsProxyHeaderToBackend(t *testing.T) {
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

	task := &model.Tunnel{
		Mode: "secret",
		// A negative client id is served by the server itself, so the
		// connection is proxied locally even without link information.
		Client: &model.Client{
			Id:   -1,
			Cnf:  &model.Config{},
			Flow: &model.Flow{},
		},
		Flow:   &model.Flow{},
		Target: &model.Target{TargetStr: backend.Addr().String(), LocalProxy: true, ProxyProtocol: 1},
	}
	bridge := &localDialBridge{links: make(chan *conn.Link, 1)}
	s := NewSecretServer(bridge, task, NewSecretPolicy(task, task.Target, true, false, false), true)

	visitor, src := tcpConnPair(t)
	done := make(chan struct{})
	go func() {
		// A link without information: the task's target.
		_ = s.Serve(src, nil, legacy.WriteAck)
		close(done)
	}()

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

	task := &model.Tunnel{
		Mode:       "secret",
		TargetType: "all",
		Client: &model.Client{
			Id:   -1,
			Cnf:  &model.Config{},
			Flow: &model.Flow{},
		},
		Flow:   &model.Flow{},
		Target: &model.Target{TargetStr: backend.LocalAddr().String(), ProxyProtocol: 2},
	}
	bridge := &localDialBridge{links: make(chan *conn.Link, 1)}
	s := NewSecretServer(bridge, task, NewSecretPolicy(task, task.Target, true, false, false), true)

	visitor, src := tcpConnPair(t)
	done := make(chan struct{})
	lk := conn.NewLink("udp", "", false, false, visitor.LocalAddr().String(), false)
	go func() {
		_ = s.Serve(src, lk, legacy.WriteAck)
		close(done)
	}()
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

func TestSecretLinkDestinationDenied(t *testing.T) {
	bridge := &localDialBridge{links: make(chan *conn.Link, 1)}
	task := &model.Tunnel{Client: &model.Client{Id: 1, Cnf: &model.Config{}, Flow: &model.Flow{}}, Flow: &model.Flow{}, Target: &model.Target{}, DestAclMode: model.AclBlacklist, DestAclRules: "denied.example"}
	s := NewSecretServer(bridge, task, NewSecretPolicy(task, task.Target, true, true, false), true)
	_, srv := tcpConnPair(t)
	if err := s.Serve(srv, conn.NewLink("tcp", "denied.example:80", false, false, "", false), legacy.WriteAck); !errors.Is(err, ErrSecretDestDenied) {
		t.Fatalf("link to a denied destination: %v", err)
	}
	select {
	case <-bridge.links:
		t.Fatal("denied destination was dialed")
	default:
	}
}

// The policy decides the same after a trip over the wire, as the provider
// applies it on the direct path. The task's destination list is the
// visitor's scope: without one only the task's targets, with one the
// destinations it allows; a refused destination is never replaced.
func TestSecretPolicyAdmit(t *testing.T) {
	listed := &model.Tunnel{Client: &model.Client{Id: 1}, TargetType: "all", DestAclMode: model.AclBlacklist, DestAclRules: "denied.example"}
	unlisted := &model.Tunnel{Client: &model.Client{Id: 1}, TargetType: "tcp"}
	tcpOnly := &model.Tunnel{Client: &model.Client{Id: 1}, TargetType: "tcp", DestAclMode: model.AclWhitelist, DestAclRules: "a"}
	target := &model.Target{TargetStr: "127.0.0.1:80\n127.0.0.1:81"}
	link := func(typ, host string, local bool) *conn.Link { return conn.NewLink(typ, host, false, false, "", local) }
	for _, tc := range []struct {
		name                            string
		task                            *model.Tunnel
		allowLocalProxy, local, anyDest bool
		lk                              *conn.Link
		want                            SecretRoute
		denied, refused                 bool
	}{
		{name: "no list: no link info", task: unlisted, lk: nil, want: SecretRoute{ConnType: "tcp"}},
		{name: "no list: no host", task: unlisted, lk: link("tcp", "", false), want: SecretRoute{ConnType: "tcp"}},
		{name: "no list: network outside", task: unlisted, lk: link("udp", "", false), denied: true},
		{name: "no list: addressed udp", task: unlisted, lk: link(conn.LinkUDP5, "", false), denied: true},
		{name: "no list: a target", task: unlisted, lk: link("tcp", "127.0.0.1:81", false), want: SecretRoute{ConnType: "tcp", Host: "127.0.0.1:81"}},
		{name: "no list: other host", task: unlisted, lk: link("tcp", "other:1", false), denied: true},
		{name: "no list, legacy any", task: unlisted, anyDest: true, lk: link("tcp", "other:1", false), want: SecretRoute{ConnType: "tcp", Host: "other:1"}},
		{name: "list: host", task: listed, lk: link("udp", "other:1", false), want: SecretRoute{ConnType: "udp", Host: "other:1"}},
		{name: "list: denied host", task: listed, lk: link("tcp", "denied.example:80", false), denied: true},
		{name: "list: network outside", task: tcpOnly, lk: link("udp", "a:1", false), denied: true},
		{name: "list: network inside", task: tcpOnly, lk: link("tcp", "a:1", false), want: SecretRoute{ConnType: "tcp", Host: "a:1"}},
		{name: "file link", task: listed, lk: link("file", "file://x", false), refused: true},
		{name: "local not allowed", task: listed, allowLocalProxy: true, lk: link("tcp", "a:1", true), want: SecretRoute{ConnType: "tcp", Host: "a:1"}},
		{name: "local allowed", task: listed, allowLocalProxy: true, local: true, lk: link("tcp", "a:1", true), want: SecretRoute{ConnType: "tcp", Host: "a:1", LocalProxy: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewSecretPolicy(tc.task, target, tc.allowLocalProxy, tc.local, tc.anyDest)
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var wire SecretPolicy
			if err := json.Unmarshal(b, &wire); err != nil {
				t.Fatal(err)
			}
			if err := wire.Compile(); err != nil {
				t.Fatal(err)
			}
			for side, p := range map[string]*SecretPolicy{"nps": p, "provider": &wire} {
				g, err := p.Admit(tc.lk)
				switch {
				case tc.denied && !errors.Is(err, ErrSecretDestDenied),
					tc.refused && err == nil,
					!tc.denied && !tc.refused && (err != nil || g != tc.want):
					t.Fatalf("%s: Admit = %+v, %v; want %+v, denied %t, refused %t", side, g, err, tc.want, tc.denied, tc.refused)
				}
			}
			if wire.Targets != target.TargetStr {
				t.Fatalf("targets = %q", wire.Targets)
			}
		})
	}
	own := NewSecretPolicy(&model.Tunnel{Client: &model.Client{Id: -1}}, target, false, true, true)
	if own.Direct() {
		t.Fatal("a task of the server's own client offers a direct path")
	}
}
