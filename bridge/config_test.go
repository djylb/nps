package bridge

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/version"
)

func TestNewConfigIgnoresClientIdentity(t *testing.T) {
	_ = file.GetDb()
	useTestDb(t)
	existing := file.NewClient("existing-vkey", false, false)
	existing.Id = 7
	if err := file.GetDb().NewClient(existing); err != nil {
		t.Fatal(err)
	}

	b := NewTunnel(false, &sync.Map{}, 30)
	clientSide, serverSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(conn.NewConn(serverSide), true, nil, version.GetLatestIndex(), "", "node-1")
	}()

	c := conn.NewConn(clientSide)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	sent := file.NewClient(existing.VerifyKey, true, true)
	sent.Id = existing.Id
	if _, err := c.SendInfo(sent, common.FlagNewConf); err != nil {
		t.Fatal(err)
	}
	if !c.GetAddStatus() {
		t.Fatal("config was not accepted")
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.Close()
	<-done

	if string(buf) == existing.VerifyKey {
		t.Fatal("server returned the submitted verify key")
	}
	stored, err := file.GetDb().GetClient(existing.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored != existing || stored.VerifyKey != "existing-vkey" || stored.NoStore {
		t.Fatalf("stored client %d changed: %+v", existing.Id, stored)
	}
	id, err := file.GetDb().GetClientIdByBlake2bVkey(crypt.Blake2b(string(buf)))
	if err != nil || id == existing.Id {
		t.Fatalf("new client id = %d, %v", id, err)
	}
}
