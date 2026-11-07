package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/acl"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/ipreg"
)

// echoBridge records the links it opens and echoes what is written to them.
type echoBridge struct {
	server bool
	links  chan *conn.Link
	peers  chan net.Conn
}

func (b *echoBridge) Session(int) conn.ClientSession { return b }

func (b *echoBridge) OpenLink(link *conn.Link, _ int) (net.Conn, error) {
	b.links <- link
	local, peer := net.Pipe()
	if b.peers != nil {
		b.peers <- peer
		return local, nil
	}
	go func() {
		defer func() { _ = peer.Close() }()
		_, _ = io.Copy(peer, peer)
	}()
	return local, nil
}

func (b *echoBridge) IsServer() bool { return b.server }

func (b *echoBridge) GlobalSource() *acl.Source { return nil }

func (b *echoBridge) CliProcess(*conn.Conn, string) {}

func waitFlow(t *testing.T, f *model.Flow, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := f.Values(); v.InletFlow > 0 && v.ExportFlow > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s flow not counted: %+v", name, f.Values())
}

func (b *echoBridge) LinkCert() *tls.Certificate {
	if b.server {
		return &tls.Certificate{}
	}
	return nil
}

func TestSocks5ConnectRelaysThroughTunnel(t *testing.T) {
	for _, tc := range []struct {
		name string
		dst  netx.Addr
	}{
		{"ipv4", netx.Addr{IP: netip.MustParseAddr("192.0.2.1"), Port: 80}},
		{"ipv6", netx.Addr{IP: netip.MustParseAddr("2001:db8::1"), Port: 443}},
		{"domain", netx.Addr{Name: "example.com", Port: 8080}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := freeTCPPort(t)
			task := newTestSocks5Task(port, "mixProxy")
			bridge := &echoBridge{links: make(chan *conn.Link, 1)}
			server, errCh := startTestTunnelModeServer(t, task, bridge)
			port = task.Port
			defer stopTestTunnelModeServer(t, server, errCh)

			c := dialSocks5NoAuth(t, port)
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			if err := socks5.WriteRequest(c, socks5.CmdConnect, tc.dst); err != nil {
				t.Fatal(err)
			}
			bound, err := socks5.ReadReply(c)
			if err != nil {
				t.Fatalf("connect reply: %v", err)
			}
			if want := "127.0.0.1:" + strconv.Itoa(port); bound.String() != want {
				t.Fatalf("bound = %v, want %s", bound, want)
			}
			link := <-bridge.links
			if link.Host != tc.dst.String() || link.ConnType != "tcp" {
				t.Fatalf("link = %s %s, want tcp %s", link.ConnType, link.Host, tc.dst)
			}
			if _, err := c.Write([]byte("hello")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 5)
			if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
				t.Fatalf("echo = %q, %v", got, err)
			}
			_ = c.Close()
			waitFlow(t, task.Flow, "task")
			waitFlow(t, task.Client.Flow, "client")
		})
	}
}

func TestSocks5ConnectDestinationDenied(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	task.DestAclMode = model.AclWhitelist
	task.DestAclRules = "192.0.2.1"
	task.CompileDestACL()
	bridge := &echoBridge{links: make(chan *conn.Link, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	port = task.Port
	defer stopTestTunnelModeServer(t, server, errCh)

	c := dialSocks5NoAuth(t, port)
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if err := socks5.WriteRequest(c, socks5.CmdConnect, netx.Addr{IP: netip.MustParseAddr("198.51.100.1"), Port: 80}); err != nil {
		t.Fatal(err)
	}
	if rep := readSocks5ReplyCode(t, c); rep != socks5.ReplyNotAllowed {
		t.Fatalf("reply = %v, want %v", rep, socks5.ReplyNotAllowed)
	}
	select {
	case link := <-bridge.links:
		t.Fatalf("denied destination opened link to %s", link.Host)
	default:
	}
}

// startSocks5UDP opens a UDP association on a new server and returns the
// tunnel peer, the client's UDP socket and the server's UDP address.
func startSocks5UDP(t *testing.T, task *model.Tunnel, server bool) (*netx.FramedConn, *net.UDPConn, *net.UDPAddr) {
	t.Helper()
	bridge := &echoBridge{server: server, links: make(chan *conn.Link, 1), peers: make(chan net.Conn, 1)}
	s, errCh := startTestTunnelModeServer(t, task, bridge)
	t.Cleanup(func() { stopTestTunnelModeServer(t, s, errCh) })

	c := dialSocks5NoAuth(t, task.Port)
	t.Cleanup(func() { _ = c.Close() })
	requestUDPAssociate(t, c)
	var peer net.Conn
	select {
	case peer = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}
	t.Cleanup(func() { _ = peer.Close() })
	if link := <-bridge.links; link.ConnType != "udp5" {
		t.Fatalf("link type = %s, want udp5", link.ConnType)
	}
	u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	return netx.NewFramedConn(peer, netx.WithFramedDatagrams()), u, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: task.Port}
}

func sendDatagram(t *testing.T, u *net.UDPConn, to *net.UDPAddr, frag byte, dst netx.Addr, payload string) {
	t.Helper()
	b, err := socks5.AppendDatagram(nil, dst, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	b[2] = frag
	if _, err := u.WriteToUDP(b, to); err != nil {
		t.Fatal(err)
	}
}

// readTunneled returns the payload of the next datagram in the tunnel, or ""
// when none comes within wait.
func readTunneled(t *testing.T, peer *netx.FramedConn, wait time.Duration) string {
	t.Helper()
	buf := make([]byte, netx.MaxFramePayload)
	_ = peer.SetReadDeadline(time.Now().Add(wait))
	n, err := peer.Read(buf)
	if err != nil {
		return ""
	}
	d, err := readSocks5Datagram(buf[:n])
	if err != nil {
		t.Fatalf("tunneled datagram: %v", err)
	}
	return string(d.data)
}

func TestSocks5UDPDropsFragmentsAndDeniedDestinations(t *testing.T) {
	task := newTestSocks5Task(freeTCPPort(t), "mixProxy")
	task.DestAclMode = model.AclWhitelist
	task.DestAclRules = "192.0.2.53"
	task.CompileDestACL()
	peer, u, to := startSocks5UDP(t, task, false)
	allowed := netx.Addr{IP: netip.MustParseAddr("192.0.2.53"), Port: 53}

	sendDatagram(t, u, to, 1, allowed, "fragment")
	sendDatagram(t, u, to, 0, netx.Addr{IP: netip.MustParseAddr("198.51.100.1"), Port: 53}, "denied")
	sendDatagram(t, u, to, 0, allowed, "ok")
	if got := readTunneled(t, peer, time.Second); got != "ok" {
		t.Fatalf("first tunneled datagram = %q, want ok", got)
	}

	// The reply reaches the client with its source.
	reply, _ := socks5.AppendDatagram(nil, netx.Addr{IP: netip.MustParseAddr("192.0.2.53"), Port: 53}, []byte("answer"))
	if _, err := peer.Write(reply); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = u.SetReadDeadline(time.Now().Add(time.Second))
	n, err := u.Read(buf)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if d, err := readSocks5Datagram(buf[:n]); err != nil || string(d.data) != "answer" || d.addr.String() != "192.0.2.53:53" {
		t.Fatalf("reply = %+v, %v", d, err)
	}
	waitFlow(t, task.Flow, "task")
	waitFlow(t, task.Client.Flow, "client")
}

func TestSocks5UDPDropsDatagramsOfDeniedSource(t *testing.T) {
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	ipreg.Register(1, netip.MustParseAddr("127.0.0.1"), time.Hour)
	task := newTestSocks5Task(freeTCPPort(t), "socks5")
	task.RegisteredOnly = true
	peer, u, to := startSocks5UDP(t, task, true)
	dst := netx.Addr{IP: netip.MustParseAddr("192.0.2.53"), Port: 53}

	sendDatagram(t, u, to, 0, dst, "before")
	if got := readTunneled(t, peer, time.Second); got != "before" {
		t.Fatalf("tunneled datagram = %q, want before", got)
	}
	// The registration ends while the association lasts.
	ipreg.Reset()
	sendDatagram(t, u, to, 0, dst, "after")
	if got := readTunneled(t, peer, 300*time.Millisecond); got != "" {
		t.Fatalf("datagram %q of a denied source entered the tunnel", got)
	}
}

func TestSocks5UDPAssociateFailsWithoutTunnel(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "socks5")
	server, errCh := startTestTunnelModeServer(t, task, failBridge{})
	port = task.Port
	defer stopTestTunnelModeServer(t, server, errCh)

	c := dialSocks5NoAuth(t, port)
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if err := socks5.WriteRequest(c, socks5.CmdUDPAssociate, netx.Addr{}); err != nil {
		t.Fatal(err)
	}
	if rep := readSocks5ReplyCode(t, c); rep != socks5.ReplyGeneralFailure {
		t.Fatalf("reply = %v, want %v", rep, socks5.ReplyGeneralFailure)
	}
}

func TestSocks5MultiAccount(t *testing.T) {
	task := newTestSocks5Task(1080, "socks5")
	task.MultiAccount = &model.MultiAccount{AccountMap: map[string]string{"alice": "pw"}}
	for _, tc := range []struct {
		user, pass string
		ok         bool
	}{{"alice", "pw", true}, {"alice", "bad", false}, {"bob", "pw", false}} {
		c, done := startSocks5Handshake(t, task)
		if err := socks5.WriteMethods(c, socks5.MethodUserPass); err != nil {
			t.Fatal(err)
		}
		if m, err := socks5.ReadMethod(c); err != nil || m != socks5.MethodUserPass {
			t.Fatalf("method %v, %v", m, err)
		}
		if err := socks5.WriteUserPass(c, tc.user, tc.pass); err != nil {
			t.Fatal(err)
		}
		if err := socks5.ReadUserPassStatus(c); (err == nil) != tc.ok {
			t.Fatalf("%s/%s: status %v, want ok=%v", tc.user, tc.pass, err, tc.ok)
		}
		_ = c.Close()
		waitSocks5Done(t, done)
	}
}
