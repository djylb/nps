package conn

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
)

func TestHandleUdp5RelaysDatagrams(t *testing.T) {
	for _, network := range []struct{ net, addr string }{{"udp4", "127.0.0.1:0"}, {"udp6", "[::1]:0"}} {
		t.Run(network.net, func(t *testing.T) {
			echo, err := net.ListenPacket(network.net, network.addr)
			if err != nil {
				t.Skipf("no %s loopback: %v", network.net, err)
			}
			defer func() { _ = echo.Close() }()
			go func() {
				buf := make([]byte, 65536)
				for {
					n, from, err := echo.ReadFrom(buf)
					if err != nil {
						return
					}
					_, _ = echo.WriteTo(append([]byte("re:"), buf[:n]...), from)
				}
			}()

			tunnel, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				HandleUdp5(ctx, tunnel, 5*time.Second, "")
				close(done)
			}()
			framed := netx.NewFramedConn(peer, netx.WithFramedDatagrams())
			_ = peer.SetDeadline(time.Now().Add(3 * time.Second))

			target := echo.LocalAddr().(*net.UDPAddr).AddrPort()
			dst := socks5.Addr{IP: target.Addr(), Port: target.Port()}

			// A fragment is dropped; the datagram after it still goes through.
			frag := []byte{0, 0, 1, 1, 127, 0, 0, 1, 0, 9, 'x'}
			if _, err := framed.Write(frag); err != nil {
				t.Fatal(err)
			}
			for _, payload := range [][]byte{[]byte("ping"), bytes.Repeat([]byte{0xa5}, 8000)} {
				pkt, err := socks5.AppendDatagram(nil, dst, payload)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := framed.Write(pkt); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, netx.MaxFramePayload)
				n, err := framed.Read(buf)
				if err != nil {
					t.Fatalf("read reply: %v", err)
				}
				from, hlen, err := socks5.DecodeDatagram(buf[:n])
				if err != nil {
					t.Fatalf("decode reply: %v", err)
				}
				if netip.AddrPortFrom(from.IP, from.Port) != target {
					t.Fatalf("reply source = %v, want %v", from, target)
				}
				if want := append([]byte("re:"), payload...); !bytes.Equal(buf[hlen:n], want) {
					t.Fatalf("reply payload of %d bytes, want %d", n-hlen, len(want))
				}
			}
			_ = peer.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("HandleUdp5 did not return after the tunnel closed")
			}
		})
	}
}
