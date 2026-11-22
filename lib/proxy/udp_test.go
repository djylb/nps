package proxy

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxyproto"
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
		case err := <-errCh:
			t.Logf("Start returned %v", err)
		case <-time.After(2 * time.Second):
			t.Error("server did not stop in time")
		}
	}()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	serverAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: server.listener.Addr().(*net.UDPAddr).Port}

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

// TestUdpModeServerProxyProtocolEach checks, for both PROXY protocol
// versions, that the header carrying the visitor's address precedes every
// datagram with ProxyProtocolEach and only the first one without it.
func TestUdpModeServerProxyProtocolEach(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, each := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d/each=%v", version, each), func(t *testing.T) {
				testUdpProxyProtocol(t, version, each)
			})
		}
	}
}

func testUdpProxyProtocol(t *testing.T, version int, each bool) {
	task := newTestSocks5Task(0, "udp")
	task.Target.TargetStr = "127.0.0.1:53"
	task.Target.ProxyProtocol = version
	task.Target.ProxyProtocolEach = each
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
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not stop in time")
		}
	}()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	clientAddr := client.LocalAddr().(*net.UDPAddr)
	serverAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: server.listener.Addr().(*net.UDPAddr).Port}

	var peer *netx.FramedConn
	buf := make([]byte, netx.MaxFramePayload)
	for i := range 3 {
		payload := bytes.Repeat([]byte{byte('a' + i)}, 10+i)
		if _, err := client.WriteToUDP(payload, serverAddr); err != nil {
			t.Fatalf("WriteToUDP() error = %v", err)
		}
		if peer == nil {
			select {
			case c := <-bridge.peers:
				t.Cleanup(func() { _ = c.Close() })
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				peer = netx.NewFramedConn(c, netx.WithFramedDatagrams())
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for udp tunnel")
			}
		}
		n, err := peer.Read(buf)
		if err != nil {
			t.Fatalf("datagram %d: %v", i, err)
		}
		got := buf[:n]
		if i == 0 || each {
			h, hn, err := proxyproto.Decode(got)
			if err != nil {
				t.Fatalf("datagram %d: no PROXY header: %v", i, err)
			}
			if int(h.Version) != version || h.Source == nil || h.Source.String() != clientAddr.String() {
				t.Fatalf("datagram %d: header version %d source %v; want v%d from %v", i, h.Version, h.Source, version, clientAddr)
			}
			got = got[hn:]
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("datagram %d: payload %q, want %q", i, got, payload)
		}
	}
}
