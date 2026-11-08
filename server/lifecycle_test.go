package server

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/server/connection"
)

// TestServerLifecycle starts a server, connects a client to its bridge and
// closes the server, twice in one process: once Close returns, no goroutine
// of the server or its bridge is left.
func TestServerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	for i := range 2 {
		t.Run(strconv.Itoa(i), testServerLifecycle)
	}
}

func testServerLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)
	const vkey = "lifecycle-key"
	c := model.NewClient(vkey, true, false)
	if err := db.NewClient(c); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	old := connection.Network()
	t.Cleanup(func() { connection.Configure(old) })
	n := old
	n.BridgeTCPIP, n.BridgeTCPPort = "127.0.0.1", port
	n.WebPort, n.HTTPProxyPort, n.HTTPSProxyPort, n.HTTP3ProxyPort, n.P2PPort = 0, 0, 0, 0, 0
	connection.Configure(n)

	srv := New(db, Config{DisconnectTimeout: 30, Transports: bridge.Transports{TCP: true}})
	if err := srv.Start(context.Background()); err != nil {
		_ = srv.Close()
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rp := client.NewRPClient("127.0.0.1:"+strconv.Itoa(port), vkey, "tcp", "", "", "", nil, 30, nil)
	rpDone := make(chan struct{})
	go func() {
		defer close(rpDone)
		rp.Start(ctx)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !srv.ClientConnected(c.Id) {
		if time.Now().After(deadline) {
			t.Fatal("the client did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if left := serverGoroutines(); left != "" {
		t.Fatalf("goroutines left after Close:\n%s", left)
	}
	if srv.ClientConnected(c.Id) {
		t.Fatal("the client is still connected after Close")
	}
	if err := srv.AddTunnel(&model.Tunnel{Mode: "secret"}); err == nil {
		t.Fatal("a closed server started a task")
	}

	rp.Close()
	cancel()
	select {
	case <-rpDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the client did not stop")
	}
}

// serverGoroutines returns the stacks of the goroutines running code of the
// server or its bridge, other than the test's own.
func serverGoroutines() string {
	var b bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&b, 2)
	var left []string
	for g := range strings.SplitSeq(b.String(), "\n\n") {
		if strings.Contains(g, "testing.tRunner") || strings.Contains(g, "testing.(*M).Run") {
			continue // the test itself
		}
		for line := range strings.SplitSeq(g, "\n") {
			if strings.HasPrefix(line, "created by") || strings.Contains(line, "testServerLifecycle") {
				continue
			}
			if strings.Contains(line, "djylb/nps/server.") || strings.Contains(line, "djylb/nps/bridge.") {
				left = append(left, g)
				break
			}
		}
	}
	return strings.Join(left, "\n\n")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}
