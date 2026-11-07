package proxy

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/ipreg"
)

func useTestDb(t *testing.T, global *model.Glob) {
	t.Helper()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(t.TempDir())}
	db.JsonDb.Global = global
	t.Cleanup(file.ReplaceDb(db))
	oldSource := globalSource.Load()
	SetGlobalSource(func() *acl.Source {
		if g := file.GetDb().GetGlobal(); g != nil {
			return g.SourceACL()
		}
		return nil
	})
	t.Cleanup(func() {
		globalSource.Store(oldSource)
	})
}

func tcpAddr(s string) net.Addr {
	a, _ := net.ResolveTCPAddr("tcp", s)
	return a
}

func TestSourceDeniedLevels(t *testing.T) {
	g := new(model.Glob)
	g.SetSourceLists(acl.DenyFirst, []string{"1.1.1.1"}, nil, acl.ScopeGlobal, "global")
	useTestDb(t, g)

	client := &model.Client{Id: 3}
	client.SetSourceLists(acl.DenyFirst, nil, []string{"1.1.1.0/24", "2.2.2.0/24"}, acl.ScopeUser, "client")
	task := &model.Tunnel{Id: 5, Client: client}
	task.SetSourceLists(acl.DenyFirst, []string{"2.2.2.2"}, nil, acl.ScopeUser, "tunnel")

	for addr, denied := range map[string]bool{
		"1.1.1.1:80": true,  // global blacklist
		"3.3.3.3:80": true,  // not on the client whitelist
		"2.2.2.2:80": true,  // tunnel blacklist
		"2.2.2.3:80": false, // allowed by every level
		"1.1.1.9:80": false,
	} {
		if got := SourceDenied(tcpAddr(addr), client, taskLists(task), taskName(task)); got != denied {
			t.Errorf("SourceDenied(%s) = %v, want %v", addr, got, denied)
		}
		if got := SourceDeniedString(addr, client, taskLists(task), taskName(task)); got != denied {
			t.Errorf("SourceDeniedString(%s) = %v, want %v", addr, got, denied)
		}
		if got := datagramDenied(tcpAddr(addr), client, taskLists(task)); got != denied {
			t.Errorf("datagramDenied(%s) = %v, want %v", addr, got, denied)
		}
	}
}

func TestSourceDeniedWithoutLists(t *testing.T) {
	useTestDb(t, nil)
	if SourceDenied(tcpAddr("1.2.3.4:5"), &model.Client{}, nil, "tunnel") {
		t.Fatal("no lists denied an address")
	}
	if SourceDenied(tcpAddr("1.2.3.4:5"), nil, nil, "tunnel") {
		t.Fatal("missing client denied an address")
	}
}

func TestSourceDeniedHostEntry(t *testing.T) {
	useTestDb(t, nil)
	h := &model.Host{Id: 7, Host: "a.com", Client: &model.Client{Id: 1}, Flow: &model.Flow{}, Target: &model.Target{}}
	h.SetSourceLists(acl.DenyFirst, nil, []string{"10.0.0.0/8"}, acl.ScopeUser, "host")
	tn := model.NewTunnelByHost(h, 443)
	if !SourceDenied(tcpAddr("8.8.8.8:1"), h.Client, taskLists(tn), taskName(tn)) {
		t.Fatal("host whitelist not applied to its HTTPS tunnel")
	}
	if taskName(tn) != "host" {
		t.Fatalf("taskName = %q, want host", taskName(tn))
	}
}

func TestSourceDeniedRegisteredOnly(t *testing.T) {
	useTestDb(t, nil)
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	client := &model.Client{Id: 3}
	task := &model.Tunnel{Id: 5, Client: client}
	task.RegisteredOnly = true
	other := &model.Tunnel{Id: 6, Client: client}

	if !SourceDenied(tcpAddr("192.0.2.1:80"), client, taskLists(task), taskName(task)) {
		t.Fatal("unregistered address passed a registered-only tunnel")
	}
	if SourceDenied(tcpAddr("192.0.2.1:80"), client, taskLists(other), taskName(other)) {
		t.Fatal("registration required by a tunnel that does not ask for it")
	}
	ipreg.Register(4, netip.MustParseAddr("192.0.2.1"), time.Hour)
	if !SourceDenied(tcpAddr("192.0.2.1:80"), client, taskLists(task), taskName(task)) {
		t.Fatal("registration for another client accepted")
	}
	ipreg.Register(3, netip.MustParseAddr("192.0.2.1"), time.Hour)
	if SourceDenied(tcpAddr("192.0.2.1:80"), client, taskLists(task), taskName(task)) || datagramDenied(tcpAddr("192.0.2.1:80"), client, taskLists(task)) {
		t.Fatal("registered address denied")
	}
	if !SourceDeniedString("192.0.2.2:80", client, taskLists(task), taskName(task)) {
		t.Fatal("unregistered address passed by text")
	}

	client.RegisteredOnly = true
	if !SourceDenied(tcpAddr("192.0.2.2:80"), client, taskLists(other), taskName(other)) {
		t.Fatal("client-level registration not required")
	}
	ipreg.Register(ipreg.AnyClient, netip.MustParseAddr("192.0.2.2"), time.Hour)
	if SourceDenied(tcpAddr("192.0.2.2:80"), client, taskLists(other), taskName(other)) {
		t.Fatal("registration for every client not accepted")
	}
}

func TestSocks5UDPDatagramDestination(t *testing.T) {
	task := &model.Tunnel{Id: 8, Mode: "mixProxy", DestAclMode: model.AclWhitelist, DestAclRules: "192.0.2.53\nfull:dns.example"}
	task.CompileDestACL()
	s := &TunnelModeServer{BaseServer: &BaseServer{Task: task}}
	for addr, want := range map[netx.Addr]bool{
		{IP: netip.MustParseAddr("192.0.2.53"), Port: 53}:   true,
		{Name: "dns.example", Port: 53}:                     true,
		{IP: netip.MustParseAddr("198.51.100.1"), Port: 53}: false,
		{Name: "other.example", Port: 443}:                  false,
	} {
		if got := s.allowsSocks5Datagram(nil, addr); got != want {
			t.Errorf("datagram to %s allowed = %v, want %v", addr, got, want)
		}
	}
	task.DestAclMode = model.AclOff
	if !s.allowsSocks5Datagram(nil, netx.Addr{IP: netip.MustParseAddr("198.51.100.1"), Port: 53}) {
		t.Fatal("datagram dropped with the destination list off")
	}
}

func TestProcessMixDeniesSourceBeforeHandshake(t *testing.T) {
	useTestDb(t, nil)
	client := &model.Client{Id: 3, Cnf: &model.Config{U: "u", P: "p"}}
	task := &model.Tunnel{Id: 9, Mode: "mixProxy", Client: client}
	task.SetSourceLists(acl.DenyFirst, []string{"127.0.0.1"}, nil, acl.ScopeUser, "tunnel 9")
	server, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- ProcessMix(conn.NewConn(&remoteConn{Conn: server, remote: tcpAddr("127.0.0.1:5000")}), &TunnelModeServer{BaseServer: &BaseServer{Bridge: &localDialBridge{}, Task: task}})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errProxyAccessDenied) {
			t.Fatalf("ProcessMix error = %v, want errProxyAccessDenied before reading the handshake", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessMix waited for a handshake from a denied address")
	}
}

type remoteConn struct {
	net.Conn
	remote net.Addr
}

func (c *remoteConn) RemoteAddr() net.Addr { return c.remote }
