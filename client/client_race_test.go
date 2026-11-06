package client

import (
	"net"
	"sync"
	"testing"

	"github.com/djylb/nps/bridge/legacy"
)

// Closing clears the tunnel while other goroutines look at it.
func TestTunnelReadWhileClosing(t *testing.T) {
	for range 200 {
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		s := NewRPClient("", "", "", "", "", "", nil, 0, nil)
		tun := legacy.NewMuxCarrier(a, "tcp", 30, true)
		s.tunnel.Store(&tun)
		var wg sync.WaitGroup
		wg.Go(func() {
			s.isTunnelClosed()
			s.snapshotCloseReason()
		})
		s.closeTunnel()
		wg.Wait()
		if !s.isTunnelClosed() {
			t.Fatal("tunnel still open")
		}
	}
}
