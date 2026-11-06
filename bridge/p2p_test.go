package bridge

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/djylb/nps/lib/version"
)

var initCert sync.Once

// newTestP2PBridge returns a bridge with a P2P service on loopback.
func newTestP2PBridge(t *testing.T) *Bridge {
	t.Helper()
	initCert.Do(func() { crypt.InitTls(tls.Certificate{}) })
	b := NewTunnel(&sync.Map{}, 30)
	err := b.StartP2P(P2PConfig{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.StopP2P)
	return b
}

// useTestDb replaces the database with an empty one in a temporary
// directory.
func useTestDb(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := file.Db
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	t.Cleanup(func() { file.Db = old })
}

// visitorRequest sends req on a WorkP2P stream served by b and returns the
// reply.
func visitorRequest(t *testing.T, b *Bridge, vs string, req p2pproto.Request) (p2pproto.Reply, error) {
	t.Helper()
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleP2PVisitor(conn.NewConn(server), 1, version.GetLatestIndex(), vs, "node-1", "tcp")
	}()
	c := conn.NewConn(client)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var reply p2pproto.Reply
	err := p2pproto.Write(c, req)
	if err == nil {
		err = p2pproto.Read(c, &reply)
	}
	_ = client.Close()
	<-done
	return reply, err
}

func TestP2PVisitorRefusedForDisabledTask(t *testing.T) {
	useTestDb(t)
	b := newTestP2PBridge(t)
	task := &file.Tunnel{Id: 7, Mode: "p2p", Password: "secret", Status: false, Client: &file.Client{Id: 2}}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatal(err)
	}
	reply, err := visitorRequest(t, b, version.VERSION, p2pproto.Request{Password: crypt.Md5("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Code != p2pproto.CodeRefused || !strings.Contains(reply.Message, "disabled") {
		t.Fatalf("reply = %+v, want a refusal for the disabled task", reply)
	}
	if n := b.p2p.Load().co.Stats().SessionsTotal; n != 0 {
		t.Fatalf("%d sessions created for a disabled task", n)
	}
}

func TestP2PVisitorWithOldNpcIsClosed(t *testing.T) {
	b := newTestP2PBridge(t)
	if _, err := visitorRequest(t, b, "0.34.7", p2pproto.Request{}); err == nil {
		t.Fatal("old npc got a reply")
	}
	if n := b.p2p.Load().co.Stats().SessionsTotal; n != 0 {
		t.Fatalf("%d sessions created for an old npc", n)
	}
}

func TestP2PJoinClaim(t *testing.T) {
	b := newTestP2PBridge(t)
	svc := b.p2p.Load()
	id, token, inv, drop, err := svc.invite(2)
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	joined := p2pJoined{c: conn.NewConn(server), serverIP: "198.51.100.1"}
	for _, tc := range []struct {
		name     string
		id       string
		provider int
		token    string
		want     error
	}{
		{"unknown invitation", "other", 2, token, errP2PUnknownInvite},
		{"other client", id, 3, token, errP2PUnknownInvite},
		{"wrong token", id, 2, strings.Repeat("A", len(token)), errP2PBadToken},
		{"token prefix", id, 2, token[:len(token)-1], errP2PBadToken},
		{"empty token", id, 2, "", errP2PBadToken},
	} {
		if err := svc.claim(tc.id, tc.provider, tc.token, joined); !errors.Is(err, tc.want) {
			t.Fatalf("%s: claim = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := svc.claim(id, 2, token, joined); err != nil {
		t.Fatalf("claim = %v", err)
	}
	if err := svc.claim(id, 2, token, joined); !errors.Is(err, errP2PJoined) {
		t.Fatalf("second join = %v, want %v", err, errP2PJoined)
	}
	if got := <-inv.join; got.serverIP != "198.51.100.1" {
		t.Fatalf("handed over %+v", got)
	}

	// A join handed over to a visitor that gave up is closed with the
	// invitation.
	id2, token2, _, drop2, err := svc.invite(2)
	if err != nil {
		t.Fatal(err)
	}
	c2, s2 := net.Pipe()
	defer func() { _ = c2.Close() }()
	if err := svc.claim(id2, 2, token2, p2pJoined{c: conn.NewConn(s2)}); err != nil {
		t.Fatal(err)
	}
	drop2()
	_ = c2.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c2.Read(make([]byte, 1)); err == nil {
		t.Fatal("abandoned join stream still open")
	}
	drop()
	if err := svc.claim(id, 2, token, joined); !errors.Is(err, errP2PUnknownInvite) {
		t.Fatalf("join after the invitation ended = %v", err)
	}
}
