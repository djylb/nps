package proxy

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/djylb/netx"
)

// TestUdpModeServerRelaysLargeDatagrams sends datagrams larger than a typical
// MTU, between small ones, both ways through a UDP tunnel and checks that
// each arrives whole.
func TestUdpModeServerRelaysLargeDatagrams(t *testing.T) {
	task := newTestSocks5Task(0, "udp")
	task.Target.TargetStr = "127.0.0.1:53"
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server := NewUdpModeServer(bridge, task, false)
	if err := server.Bind(); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()
	defer func() {
		_ = server.Close()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop in time")
		}
	}()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	serverAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: server.listener.LocalAddr().(*net.UDPAddr).Port}

	var tunnel net.Conn
	defer func() {
		if tunnel != nil {
			_ = tunnel.Close()
		}
	}()
	var peer *netx.FramedConn
	buf := make([]byte, netx.MaxFramePayload)
	// 8000 bytes stay under the smallest loopback datagram limit (macOS).
	for i, size := range []int{4, 8000, 2000, 8000, 4} {
		payload := bytes.Repeat([]byte{byte('a' + i)}, size)
		if _, err := client.WriteToUDP(payload, serverAddr); err != nil {
			t.Fatalf("WriteToUDP(%d bytes) error = %v", size, err)
		}
		if peer == nil {
			select {
			case c := <-bridge.peers:
				tunnel = c
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				peer = netx.NewFramedConn(c, netx.WithFramedDatagrams())
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for udp tunnel")
			}
		}
		n, err := peer.Read(buf)
		if err != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("datagram %d: tunnel got %d bytes, %v; want %d", i, n, err, size)
		}

		reply := bytes.Repeat([]byte{byte('A' + i)}, size)
		if _, err := peer.Write(reply); err != nil {
			t.Fatalf("write reply %d: %v", i, err)
		}
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err = client.ReadFromUDP(buf)
		if err != nil || !bytes.Equal(buf[:n], reply) {
			t.Fatalf("reply %d: client got %d bytes, %v; want %d", i, n, err, size)
		}
	}
}
