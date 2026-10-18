package bridge

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/version"
)

// signalNode returns a node announcing vs whose signal connection is one end
// of a pipe, and the other end.
func signalNode(t *testing.T, uuid, vs string) (*Node, *conn.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	n := NewNode(uuid, vs, version.GetLatestIndex())
	n.AddSignal(conn.NewConn(a))
	return n, conn.NewConn(b)
}

func TestRequestClientUpdate(t *testing.T) {
	b := NewTunnel(&sync.Map{}, 30)

	if _, err := b.RequestClientUpdate(1); !errors.Is(err, ErrClientOffline) {
		t.Fatalf("unknown client: err = %v, want ErrClientOffline", err)
	}

	old, _ := signalNode(t, "old", "0.34.8")
	b.Client.Store(1, NewClient(1, old))
	if _, err := b.RequestClientUpdate(1); !errors.Is(err, ErrUpdateUnsupported) {
		t.Fatalf("old npc only: err = %v, want ErrUpdateUnsupported", err)
	}

	cur, peer := signalNode(t, "new", version.RemoteUpdateMinVersion)
	v, _ := b.Client.Load(1)
	v.(*Client).AddNode(cur)
	got := make(chan string, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		flag, err := peer.ReadFlag()
		if err == nil {
			_, err = peer.GetShortLenContent()
		}
		if err != nil {
			flag = err.Error()
		}
		got <- flag
	}()
	n, err := b.RequestClientUpdate(1)
	if err != nil || n != 1 {
		t.Fatalf("RequestClientUpdate = %d, %v; want 1 npc asked", n, err)
	}
	if flag := <-got; flag != common.UpdateRequest {
		t.Fatalf("npc read %q, want the update request", flag)
	}
}
