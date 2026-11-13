package bridge

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
)

// attachTest attaches to the task of password as visitor client 1 on node
// over a pipe served by b. answer, when set, replaces the proof the
// visitor sends for the challenge's nonce. A granting attach keeps its
// stream open until the test ends; the stream is returned.
func attachTest(t *testing.T, b *Bridge, password, node string, answer func(nonce []byte) legacy.SecretAttach) (legacy.SecretGranted, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleSecretAttach(conn.NewConn(server), 1, node)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	var ch legacy.SecretChallenge
	if err := legacy.ReadP2P(client, &ch); err != nil {
		t.Fatal(err)
	}
	req := legacy.SecretAttach{Index: crypt.SecretIndex(password), Proof: crypt.SecretProof(password, ch.Nonce, node)}
	if answer != nil {
		req = answer(ch.Nonce)
	}
	if err := legacy.WriteP2P(client, req); err != nil {
		t.Fatal(err)
	}
	var granted legacy.SecretGranted
	if err := legacy.ReadP2P(client, &granted); err != nil {
		return legacy.SecretGranted{Code: "closed", Message: err.Error()}, client
	}
	_ = client.SetDeadline(time.Time{})
	return granted, client
}

func newSecretTestTask(t *testing.T, b *Bridge) *model.Tunnel {
	t.Helper()
	task := &model.Tunnel{Id: 3, Mode: "secret", Password: "pw", Status: true, Client: &model.Client{Id: 2, Cnf: &model.Config{}, Flow: &model.Flow{}},
		Flow: &model.Flow{}, Target: &model.Target{TargetStr: "127.0.0.1:1"}}
	if err := b.store.NewTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

// A captured attach is good for no other: its proof covers the nonce of
// its own challenge.
func TestSecretAttachReplayFails(t *testing.T) {
	b := newTestP2PBridge(t, useTestDb(t))
	newSecretTestTask(t, b)
	var captured legacy.SecretAttach
	granted, _ := attachTest(t, b, "pw", "node-1", func(nonce []byte) legacy.SecretAttach {
		captured = legacy.SecretAttach{Index: crypt.SecretIndex("pw"), Proof: crypt.SecretProof("pw", nonce, "node-1")}
		return captured
	})
	if granted.Grant == "" {
		t.Fatalf("attach = %+v", granted)
	}
	replayed, _ := attachTest(t, b, "pw", "node-1", func([]byte) legacy.SecretAttach { return captured })
	if replayed.Grant != "" || replayed.Code != legacy.P2PCodeRefused {
		t.Fatalf("replayed attach = %+v, want a refusal", replayed)
	}
	// The proof is bound to the node too.
	other, _ := attachTest(t, b, "pw", "node-2", func(nonce []byte) legacy.SecretAttach {
		return legacy.SecretAttach{Index: crypt.SecretIndex("pw"), Proof: crypt.SecretProof("pw", nonce, "node-1")}
	})
	if other.Grant != "" {
		t.Fatalf("attach with another node's proof = %+v", other)
	}
	if wrong, _ := attachTest(t, b, "nope", "node-1", nil); wrong.Grant != "" {
		t.Fatalf("attach with a wrong password = %+v", wrong)
	}
}

// Revoking the task's grants, as stopping it does, closes the attach stream
// and makes the grant unknown to links and P2P requests; a grant is bound
// to its visitor node.
func TestSecretGrantRevokedWithTunnel(t *testing.T) {
	b := newTestP2PBridge(t, useTestDb(t))
	task := newSecretTestTask(t, b)
	granted, stream := attachTest(t, b, "pw", "node-1", nil)
	if granted.Grant == "" || granted.Tunnel != task.Id || granted.Peer != 2 || !granted.NameDest || !granted.Direct {
		t.Fatalf("attach = %+v", granted)
	}
	if b.secrets.get(granted.Grant, 1, "node-1") == nil {
		t.Fatal("grant not found")
	}
	if b.secrets.get(granted.Grant, 1, "node-2") != nil || b.secrets.get(granted.Grant, 4, "node-1") != nil {
		t.Fatal("grant found for another visitor")
	}
	g := b.secrets.get(granted.Grant, 1, "node-1")
	link, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	if !g.track(link) {
		t.Fatal("link not tracked")
	}
	b.RevokeSecretTunnel(task.Id + 1)
	if b.secrets.get(granted.Grant, 1, "node-1") == nil {
		t.Fatal("grant of another task revoked")
	}
	b.RevokeSecretTunnel(task.Id)
	_ = stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("attach stream after revocation: %v", err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("relayed link after revocation: %v", err)
	}
	if b.secrets.get(granted.Grant, 1, "node-1") != nil {
		t.Fatal("revoked grant found")
	}
}

// A tunnel that does not allow relaying refuses the relayed link of a
// grant it still issues: the visitor may go direct.
func TestSecretLinkRefusedWithoutRelay(t *testing.T) {
	b := newTestP2PBridge(t, useTestDb(t))
	task := newSecretTestTask(t, b)
	task.NoRelay = true
	granted, _ := attachTest(t, b, "pw", "node-1", nil)
	if granted.Grant == "" {
		t.Fatalf("attach = %+v", granted)
	}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleSecretLink(conn.NewConn(server), 1, "node-1")
	}()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if err := legacy.WriteP2P(client, legacy.SecretOpen{Grant: granted.Grant}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.WriteLink(client, conn.NewLink("tcp", "127.0.0.1:1", false, false, "127.0.0.1:2", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("relayed link without relay: %v", err)
	}
	<-done
	if n := b.RelayedSecretLinks(); n != 0 {
		t.Fatalf("relayed %d", n)
	}
}
