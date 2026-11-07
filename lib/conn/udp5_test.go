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
			dst := netx.Addr{IP: target.Addr(), Port: target.Port()}

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

// startUDPEcho answers each datagram with "re:" and the payload, and reports
// the source address of each datagram it got on the returned channel.
func startUDPEcho(t *testing.T, network, addr string) (net.PacketConn, <-chan netip.AddrPort) {
	t.Helper()
	echo, err := net.ListenPacket(network, addr)
	if err != nil {
		t.Skipf("no %s loopback: %v", network, err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	froms := make(chan netip.AddrPort, 16)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			select {
			case froms <- from.(*net.UDPAddr).AddrPort():
			default:
			}
			_, _ = echo.WriteTo(append([]byte("re:"), buf[:n]...), from)
		}
	}()
	return echo, froms
}

// startUdp5 runs HandleUdp5 on one end of a pipe and returns the other end,
// framed and raw, and a channel closed when HandleUdp5 returns.
func startUdp5(t *testing.T, ctx context.Context, timeout time.Duration, localIP string) (net.Conn, *netx.FramedConn, <-chan struct{}) {
	t.Helper()
	tunnel, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	done := make(chan struct{})
	go func() {
		HandleUdp5(ctx, tunnel, timeout, localIP)
		close(done)
	}()
	return peer, netx.NewFramedConn(peer, netx.WithFramedDatagrams()), done
}

func roundTripUdp5(t *testing.T, framed *netx.FramedConn, dst netx.Addr, payload []byte) netx.Addr {
	t.Helper()
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
	if want := append([]byte("re:"), payload...); !bytes.Equal(buf[hlen:n], want) {
		t.Fatalf("reply payload %q, want %q", buf[hlen:n], want)
	}
	return from
}

func waitUdp5Done(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("HandleUdp5 did not return after %s", what)
	}
}

func TestHandleUdp5DomainTarget(t *testing.T) {
	echo, _ := startUDPEcho(t, "udp4", "127.0.0.1:0")
	peer, framed, done := startUdp5(t, context.Background(), 5*time.Second, "")
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	port := echo.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	from := roundTripUdp5(t, framed, netx.Addr{Name: "localhost", Port: port}, []byte("named"))
	if from.IP != netip.MustParseAddr("127.0.0.1") || from.Port != port {
		t.Fatalf("reply source = %v", from)
	}
	// An IP datagram after the named one still goes through.
	roundTripUdp5(t, framed, netx.Addr{IP: netip.MustParseAddr("127.0.0.1"), Port: port}, []byte("ip"))
	_ = peer.Close()
	waitUdp5Done(t, done, "the tunnel closed")
}

func TestHandleUdp5BindsLocalIP(t *testing.T) {
	echo, froms := startUDPEcho(t, "udp4", "127.0.0.1:0")
	peer, framed, done := startUdp5(t, context.Background(), 5*time.Second, "127.0.0.1")
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	target := echo.LocalAddr().(*net.UDPAddr).AddrPort()
	roundTripUdp5(t, framed, netx.Addr{IP: target.Addr(), Port: target.Port()}, []byte("bound"))
	if from := <-froms; from.Addr().Unmap() != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("datagram sent from %v, want 127.0.0.1", from)
	}
	_ = peer.Close()
	waitUdp5Done(t, done, "the tunnel closed")
}

func TestHandleUdp5IdleTimeout(t *testing.T) {
	_, _, done := startUdp5(t, context.Background(), 200*time.Millisecond, "")
	waitUdp5Done(t, done, "the idle timeout")
}

func TestHandleUdp5ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	peer, _, done := startUdp5(t, ctx, 5*time.Second, "")
	cancel()
	waitUdp5Done(t, done, "the context ended")
	// The tunnel end is closed too.
	_ = peer.SetDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("tunnel still open after HandleUdp5 returned")
	}
}
