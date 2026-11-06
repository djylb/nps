package conn

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/netx"
)

// The UDP tunnel between nps and npc frames each datagram as a 2-byte
// big-endian payload length followed by the payload. These tests pin those
// bytes.

func TestFramedDatagramWireFormat(t *testing.T) {
	local, peer := net.Pipe()
	defer func() { _ = local.Close(); _ = peer.Close() }()
	framed := netx.NewFramedConn(local, netx.WithFramedDatagrams())

	big := bytes.Repeat([]byte{0x5a}, netx.MaxFramePayload)
	cases := []struct {
		name    string
		payload []byte
		want    []byte
	}{
		{"short", []byte("abc"), []byte{0x00, 0x03, 'a', 'b', 'c'}},
		{"empty", nil, []byte{0x00, 0x00}},
		{"max", big, append([]byte{0xff, 0xff}, big...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errCh := make(chan error, 1)
			go func() {
				n, err := framed.Write(tc.payload)
				if err == nil && n != len(tc.payload) {
					err = io.ErrShortWrite
				}
				errCh <- err
			}()
			got := make([]byte, len(tc.want))
			if _, err := io.ReadFull(peer, got); err != nil {
				t.Fatalf("read frame: %v", err)
			}
			if err := <-errCh; err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("frame bytes = % x..., want % x...", got[:min(len(got), 8)], tc.want[:min(len(tc.want), 8)])
			}
		})
	}

	// A datagram over the limit is refused without writing anything, so
	// the next frame is the first thing on the wire.
	if _, err := framed.Write(make([]byte, netx.MaxFramePayload+1)); !errors.Is(err, netx.ErrFrameTooLarge) {
		t.Fatalf("oversized Write() error = %v, want ErrFrameTooLarge", err)
	}
	go func() { _, _ = framed.Write([]byte("z")) }()
	got := make([]byte, 3)
	if _, err := io.ReadFull(peer, got); err != nil || !bytes.Equal(got, []byte{0x00, 0x01, 'z'}) {
		t.Fatalf("frame after oversized write = % x, %v; want 00 01 7a", got, err)
	}
}

func TestFramedDatagramReadsWireFormat(t *testing.T) {
	local, peer := net.Pipe()
	defer func() { _ = local.Close(); _ = peer.Close() }()
	framed := netx.NewFramedConn(local, netx.WithFramedDatagrams())

	go func() {
		_, _ = peer.Write([]byte{
			0x00, 0x02, 'h', 'i',
			0x00, 0x00,
			0x00, 0x05, 'w', 'o', 'r', 'l', 'd',
			0x00, 0x01, '!',
		})
	}()
	_ = local.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 16)
	if n, err := framed.Read(buf); err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("Read() = %q, %v; want hi", buf[:n], err)
	}
	if n, err := framed.Read(buf); err != nil || n != 0 {
		t.Fatalf("empty frame Read() = %d, %v; want 0, nil", n, err)
	}
	// A frame longer than the buffer is truncated, like a UDP read.
	small := make([]byte, 3)
	if n, err := framed.Read(small); err != nil || string(small[:n]) != "wor" {
		t.Fatalf("truncated Read() = %q, %v; want wor", small[:n], err)
	}
	if n, err := framed.Read(buf); err != nil || string(buf[:n]) != "!" {
		t.Fatalf("Read() after truncation = %q, %v; want !", buf[:n], err)
	}
}

// TestCopyWaitGroupFramesUDP relays datagrams between a framed tunnel and a
// UDP target as npc does, and checks the tunnel bytes in both directions.
func TestCopyWaitGroupFramesUDP(t *testing.T) {
	target, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	udpConn, err := net.DialUDP("udp", nil, target.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	tunnel, peer := net.Pipe()
	defer func() { _ = peer.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		CopyWaitGroup(tunnel, udpConn, false, false, nil, nil, false, 0, nil, nil, true, true)
	}()

	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Write([]byte{0x00, 0x04, 'p', 'i', 'n', 'g'}); err != nil {
		t.Fatalf("write tunnel frame: %v", err)
	}
	_ = target.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, from, err := target.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("target got %q, %v; want ping", buf[:n], err)
	}
	if _, err := target.WriteToUDP([]byte("pong!"), from); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 7)
	if _, err := io.ReadFull(peer, got); err != nil || !bytes.Equal(got, []byte{0x00, 0x05, 'p', 'o', 'n', 'g', '!'}) {
		t.Fatalf("tunnel got % x, %v; want 00 05 pong!", got, err)
	}

	_ = peer.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CopyWaitGroup did not return after the tunnel closed")
	}
}
