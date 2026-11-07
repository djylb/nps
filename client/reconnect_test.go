package client

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// serveNameDNS answers A and AAAA queries from ips and returns its address.
func serveNameDNS(t *testing.T, ips *atomic.Value) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			end := 12
			for end < n && buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			end += 5
			resp := append([]byte{}, buf[:end]...)
			resp[2], resp[3] = 0x81, 0x80
			resp[10], resp[11] = 0, 0
			qtype := buf[end-3]
			for _, ip := range ips.Load().([]net.IP) {
				rr := ip.To4()
				rtype := byte(1)
				if rr == nil {
					rr, rtype = ip.To16(), 28
				}
				if rtype != qtype {
					continue
				}
				resp[7]++
				resp = append(resp, 0xc0, 12, 0, rtype, 0, 1, 0, 0, 0, 1, 0, byte(len(rr)))
				resp = append(resp, rr...)
			}
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String()
}

// useTestDNS restores the process-wide resolver after the test.
func useTestDNS(t *testing.T) {
	t.Helper()
	old := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = old })
}

func TestDialTargets(t *testing.T) {
	useTestDNS(t)
	var ips atomic.Value
	ips.Store([]net.IP{net.ParseIP("::1"), net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")})
	common.SetCustomDNS(serveNameDNS(t, &ips))

	const name = "npc-test.example.invalid:8024"
	if got := dialTargets("tcp", name); !slices.Equal(got, []string{name}) {
		t.Fatalf("tcp targets = %v", got)
	}
	if got := dialTargets("kcp", "192.0.2.9:1"); !slices.Equal(got, []string{"192.0.2.9:1"}) {
		t.Fatalf("kcp targets of an IP = %v", got)
	}
	want := []string{"192.0.2.1:8024", "192.0.2.2:8024", "[::1]:8024"}
	for _, tp := range []string{"kcp", common.ConnQUIC} {
		got := dialTargets(tp, name)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("%s targets = %v, want %v", tp, got, want)
		}
	}
}

// bridgeListener serves a bridge on addr and can drop everything it accepted.
type bridgeListener struct {
	ln       net.Listener
	accepted atomic.Int32
	mu       sync.Mutex
	conns    []net.Conn
}

func listenBridge(t *testing.T, b *bridge.Bridge, addr string) *bridgeListener {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("listen %s: %v", addr, err)
	}
	l := &bridgeListener{ln: ln}
	t.Cleanup(l.stop)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			l.accepted.Add(1)
			l.mu.Lock()
			l.conns = append(l.conns, c)
			l.mu.Unlock()
			go b.CliProcess(conn.NewConn(c), "tcp")
		}
	}()
	return l
}

func (l *bridgeListener) stop() {
	_ = l.ln.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
}

// The server goes away and comes back at another address of its name: npc
// must find it there without a restart.
func TestReconnectFollowsNameToNewAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	useTestDNS(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDb := file.Db
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	t.Cleanup(func() { file.Db = oldDb })
	const vkey = "reconnect-key"
	if err := file.GetDb().NewClient(model.NewClient(vkey, true, false)); err != nil {
		t.Fatal(err)
	}
	b := bridge.NewTunnel(&sync.Map{}, 30)

	first := listenBridge(t, b, "127.0.0.1:0")
	port := first.ln.Addr().(*net.TCPAddr).Port

	var ips atomic.Value
	ips.Store([]net.IP{net.ParseIP("127.0.0.1")})
	path := filepath.Join(root, "npc.conf")
	conf := "[common]\nserver_addr=npc-test.example.invalid:" + strconv.Itoa(port) +
		"\nconn_type=tcp\nvkey=" + vkey + "\nauto_reconnection=true\ndns_server=" + serveNameDNS(t, &ips) + "\n"
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		StartFromFile(ctx, cancel, path)
	}()
	t.Cleanup(func() {
		cancel()
		first.stop()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("npc did not stop")
		}
	})

	waitFor(t, 15*time.Second, "first connection", func() bool { return first.accepted.Load() >= 2 })
	ips.Store([]net.IP{net.ParseIP("::1")})
	first.stop()
	moved := listenBridge(t, b, net.JoinHostPort("::1", strconv.Itoa(port)))
	waitFor(t, 45*time.Second, "reconnection at the new address", func() bool { return moved.accepted.Load() >= 2 })
}
