package proxy

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/rate"
)

func TestSocks5UDPTunnelHonoursClientRate(t *testing.T) {
	task := newTestSocks5Task(0, "socks5")
	task.Client.Rate = rate.NewRate(1000)
	task.Client.Rate.Start()
	defer task.Client.Rate.Stop()

	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	registry := &socks5UDPRegistry{task: task, bridge: bridge}
	control, controlPeer := net.Pipe()
	defer func() { _ = control.Close() }()
	defer func() { _ = controlPeer.Close() }()

	session := &socks5UDPSession{registry: registry, control: control, timeout: time.Minute}
	if err := session.openTunnel(); err != nil {
		t.Fatalf("openTunnel: %v", err)
	}
	peer := <-bridge.peers
	defer func() { _ = peer.Close() }()
	go func() { _, _ = io.Copy(io.Discard, peer) }()

	// 4000 bytes at 1000 B/s exceed the initial two-second burst, so the
	// write has to wait for the limiter.
	done := make(chan struct{})
	go func() {
		_, _ = session.framed.Write(make([]byte, 4000))
		close(done)
	}()
	select {
	case <-done:
		t.Fatalf("write was not rate limited")
	case <-time.After(300 * time.Millisecond):
	}
	_ = session.framed.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("close did not release the rate-limited write")
	}
}
