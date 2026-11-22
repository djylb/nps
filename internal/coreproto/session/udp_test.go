package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// bothSessions runs f on sessions with datagrams and without: Read waits
// for two sources on the first, reads the stream alone on the second.
func bothSessions(t *testing.T, f func(*testing.T, bool)) {
	t.Run("datagrams", func(t *testing.T) { f(t, true) })
	t.Run("stream-only", func(t *testing.T) { f(t, false) })
}

// zmuxPair is a client and server session over a loopback TCP connection.
func zmuxPair(tb testing.TB, datagrams bool) (cli, srv *zmux.Conn) {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	cfg := func() *zmux.Config { return &zmux.Config{EnableDatagrams: datagrams} }
	type res struct {
		c   *zmux.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			ch <- res{err: err}
			return
		}
		c, err := zmux.Server(context.Background(), nc, cfg())
		ch <- res{c, err}
	}()
	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		tb.Fatal(err)
	}
	cli, err = zmux.Client(context.Background(), nc, cfg())
	if err != nil {
		tb.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		tb.Fatal(r.err)
	}
	tb.Cleanup(func() { _ = cli.Close(); _ = srv.Close() })
	return cli, r.c
}

// rawUDPPeer is a udpConn on the client session and the bare stream of the
// other end, where a test writes and reads the bytes of §4.5 itself.
func rawUDPPeer(tb testing.TB, addressed bool) (*udpConn, *zmux.Stream, *zmux.Conn) {
	tb.Helper()
	return rawUDPPeerOn(tb, addressed, true)
}

// rawUDPPeerOn is rawUDPPeer on sessions with or without datagrams.
func rawUDPPeerOn(tb testing.TB, addressed, datagrams bool) (*udpConn, *zmux.Stream, *zmux.Conn) {
	tb.Helper()
	cli, srv := zmuxPair(tb, datagrams)
	st, err := srv.OpenStream()
	if err != nil {
		tb.Fatal(err)
	}
	// The first frame announces the stream; the test reads it back.
	prime := []byte{0}
	if addressed {
		prime = []byte{0x05, 0x04, 0x03, 'p', 0, 1} // host "p" port 1, no payload
	}
	if _, err := st.Write(prime); err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := cli.AcceptStream(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	u := newUDPConn(&linkConn{Stream: cs}, addressed, cli)
	tb.Cleanup(func() { _ = u.Close(); st.CancelRead(0); st.CancelWrite(0) })
	readFrame(tb, u)
	return u, st, srv
}

// udpPair is two udpConns facing each other.
func udpPair(tb testing.TB, addressed bool) (a, b *udpConn) {
	tb.Helper()
	return udpPairOn(tb, addressed, true)
}

func udpPairOn(tb testing.TB, addressed, datagrams bool) (a, b *udpConn) {
	tb.Helper()
	cli, srv := zmuxPair(tb, datagrams)
	st, err := srv.OpenStream()
	if err != nil {
		tb.Fatal(err)
	}
	a = newUDPConn(&linkConn{Stream: st}, addressed, srv)
	tb.Cleanup(func() { _ = a.Close() })
	first := []byte{0, 0}
	if addressed {
		first, _ = socks5.AppendDatagram([]byte{0, 0}, netx.Addr{IP: netip.MustParseAddr("10.0.0.1"), Port: 1}, nil)
		binary.BigEndian.PutUint16(first, uint16(len(first)-2))
	}
	a.datagrams = false // the announcement is in the stream
	if _, err := a.Write(first); err != nil {
		tb.Fatal(err)
	}
	a.datagrams = datagrams
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := cli.AcceptStream(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	b = newUDPConn(&linkConn{Stream: cs}, addressed, cli)
	tb.Cleanup(func() { _ = b.Close() })
	readFrame(tb, b)
	return a, b
}

// readFrame reads one frame of u and returns its payload.
func readFrame(tb testing.TB, u io.Reader) []byte {
	tb.Helper()
	if d, ok := u.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	var h [2]byte
	if _, err := io.ReadFull(u, h[:]); err != nil {
		tb.Fatal(err)
	}
	b := make([]byte, binary.BigEndian.Uint16(h[:]))
	if _, err := io.ReadFull(u, b); err != nil {
		tb.Fatal(err)
	}
	return b
}

func framed(payload []byte) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(payload))), payload...)
}

// The bytes of §4.5 on the stream: Len varint then the body; a fixed-target
// body is the payload, an addressed one AddrLen Address Payload.
func TestUDPWireGolden(t *testing.T) {
	big := bytes.Repeat([]byte{0xab}, 100)
	cases := []struct {
		name      string
		frame     []byte // what the tunnel writes
		wire      []byte // what goes on the stream
		back      []byte // the frame the wire shows, when not frame
		addressed bool
	}{
		{"fixed-short", framed([]byte("hi")), []byte{0x02, 'h', 'i'}, nil, false},
		{"fixed-empty", framed(nil), []byte{0x00}, nil, false},
		{"fixed-2byte-len", framed(big), append([]byte{0x40, 100}, big...), nil, false},
		{"addressed-v4",
			framed(append([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0x12, 0x34}, "x"...)),
			[]byte{0x09, 0x07, 0x01, 1, 2, 3, 4, 0x12, 0x34, 'x'}, nil, true},
		{"addressed-v4mapped-v6",
			framed(append([]byte{0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 1, 2, 3, 4, 0, 53}, "y"...)),
			[]byte{0x09, 0x07, 0x01, 1, 2, 3, 4, 0, 53, 'y'},
			framed(append([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53}, "y"...)), true},
		{"addressed-domain",
			framed(append([]byte{0, 0, 0, 3, 3, 'a', '.', 'b', 0, 80}, "z"...)),
			[]byte{0x08, 0x06, 0x03, 'a', '.', 'b', 0, 80, 'z'}, nil, true},
	}
	for _, tc := range cases {
		if tc.back == nil {
			tc.back = tc.frame
		}
		t.Run(tc.name, func(t *testing.T) {
			u, st, _ := rawUDPPeer(t, tc.addressed)
			u.datagrams = false
			if _, err := u.Write(tc.frame); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(tc.wire))
			_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(st, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.wire) {
				t.Fatalf("wire %x, want %x", got, tc.wire)
			}
			// Back the other way, the same bytes show the same frame.
			if _, err := st.Write(tc.wire); err != nil {
				t.Fatal(err)
			}
			if f := readFrame(t, u); !bytes.Equal(framed(f), tc.back) {
				t.Fatalf("frame %x, want %x", framed(f), tc.back)
			}
			// As a datagram the body alone, with no Len.
			u.datagrams = true
			if _, err := u.Write(tc.frame); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cx, d, err := st.ReceiveDatagram(ctx)
			if err != nil || cx != 0 {
				t.Fatal(cx, err)
			}
			if body := tc.wire[len(tc.wire)-len(d):]; !bytes.Equal(d, body) || wire.VarintLen(uint64(len(d)))+len(d) != len(tc.wire) {
				t.Fatalf("datagram %x, want %x", d, body)
			}
			if err := st.SendDatagram(0, d); err != nil {
				t.Fatal(err)
			}
			if f := readFrame(t, u); !bytes.Equal(framed(f), tc.back) {
				t.Fatalf("datagram frame %x, want %x", framed(f), tc.back)
			}
		})
	}
}

// Short reads take a frame in pieces; a frame larger than the stream reads
// at once still arrives whole.
func TestUDPReadPieces(t *testing.T) { bothSessions(t, testUDPReadPieces) }

func testUDPReadPieces(t *testing.T, dg bool) {
	u, st, _ := rawUDPPeerOn(t, false, dg)
	payload := bytes.Repeat([]byte("0123456789"), 6000)
	w := wire.AppendVarint(nil, uint64(len(payload)))
	w = append(w, payload...)
	// Written byte by byte at first, the parser resumes across reads.
	for _, b := range w[:3] {
		if _, err := st.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := st.Write(w[3:]); err != nil {
		t.Fatal(err)
	}
	_ = u.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got []byte
	buf := make([]byte, 7)
	for len(got) < len(payload)+2 {
		n, err := u.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, framed(payload)) {
		t.Fatal("frame differs")
	}
}

func TestUDPOversizedStreamPacket(t *testing.T) { bothSessions(t, testUDPOversizedStreamPacket) }

func testUDPOversizedStreamPacket(t *testing.T, dg bool) {
	for _, tc := range []struct {
		name      string
		wire      []byte
		addressed bool
	}{
		{"len-over-bound", wire.AppendVarint(nil, wire.MaxUDPPacket+1), false},
		{"fixed-payload-over", append(wire.AppendVarint(nil, wire.MaxUDPPayload+1), make([]byte, wire.MaxUDPPayload+1)...), false},
		{"addressed-payload-over", func() []byte {
			b, _ := wire.AppendUDPBody(nil, &wire.Address{IP: netip.MustParseAddr("1.2.3.4"), Port: 5}, make([]byte, wire.MaxUDPPayload+1))
			return append(wire.AppendVarint(nil, uint64(len(b))), b...)
		}(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, st, _ := rawUDPPeerOn(t, tc.addressed, dg)
			// A good frame first: it is delivered before the bad one ends the
			// stream.
			good := []byte{0x01, 'g'}
			if tc.addressed {
				good, _ = wire.AppendUDPBody(nil, &wire.Address{IP: netip.MustParseAddr("1.2.3.4"), Port: 5}, []byte("g"))
				good = append(wire.AppendVarint(nil, uint64(len(good))), good...)
			}
			if _, err := st.Write(append(good, tc.wire...)); err != nil {
				t.Fatal(err)
			}
			f := readFrame(t, u)
			if f[len(f)-1] != 'g' {
				t.Fatalf("good frame %x", f)
			}
			_ = u.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := u.Read(make([]byte, 10)); !errors.Is(err, wire.ErrMalformed) {
				t.Fatalf("read %v", err)
			}
			// The peer sees NPS_MALFORMED.
			_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, err := io.ReadAll(st)
			var se *zmux.StreamError
			if !errors.As(err, &se) || uint64(se.ErrorCode) != wire.CodeMalformed {
				t.Fatalf("peer %v", err)
			}
		})
	}
}

// Datagrams that cannot be shown are dropped, the link goes on; so is an
// addressed stream packet with a bad address.
func TestUDPDroppedPackets(t *testing.T) {
	t.Run("fixed", func(t *testing.T) {
		u, st, _ := rawUDPPeer(t, false)
		if mp := u.lc.MaxDatagramPayload(0); mp > wire.MaxUDPPayload {
			if err := st.SendDatagram(0, make([]byte, wire.MaxUDPPayload+1)); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(20 * time.Millisecond)
		if _, err := st.Write([]byte{0x01, 'k'}); err != nil {
			t.Fatal(err)
		}
		if f := readFrame(t, u); string(f) != "k" {
			t.Fatalf("%q", f)
		}
	})
	t.Run("addressed", func(t *testing.T) {
		u, st, _ := rawUDPPeer(t, true)
		bad := []byte{0x05, 0x01} // AddrLen past the end
		if err := st.SendDatagram(0, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Write(append([]byte{byte(len(bad))}, bad...)); err != nil {
			t.Fatal(err)
		}
		good, _ := wire.AppendUDPBody(nil, &wire.Address{Host: "h", Port: 9}, []byte("ok"))
		if _, err := st.Write(append([]byte{byte(len(good))}, good...)); err != nil {
			t.Fatal(err)
		}
		f := readFrame(t, u)
		a, n, err := socks5.DecodeDatagram(f)
		if err != nil || a.Name != "h" || a.Port != 9 || string(f[n:]) != "ok" {
			t.Fatalf("%x %v", f, err)
		}
	})
	t.Run("addressed-write", func(t *testing.T) {
		u, st, _ := rawUDPPeer(t, true)
		u.datagrams = false
		// A frame that is no SOCKS5 datagram is dropped on the way out.
		if _, err := u.Write(framed([]byte{0, 0, 1, 1})); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Write(framed([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 1, 'q'})); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 10)
		_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(st, got); err != nil || got[len(got)-1] != 'q' {
			t.Fatalf("%x %v", got, err)
		}
	})
}

// A truncated stream: inside the Len an EOF, inside the body an unexpected EOF.
func TestUDPTruncatedStream(t *testing.T) { bothSessions(t, testUDPTruncatedStream) }

func testUDPTruncatedStream(t *testing.T, dg bool) {
	for _, tc := range []struct {
		name string
		want error
		wire []byte
	}{
		{"boundary", io.EOF, nil},
		{"in-len", io.EOF, []byte{0x40}},
		{"before-body", io.EOF, []byte{0x05}},
		{"in-body", io.ErrUnexpectedEOF, []byte{0x05, 'a', 'b'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, st, _ := rawUDPPeerOn(t, false, dg)
			if _, err := st.Write(tc.wire); err != nil {
				t.Fatal(err)
			}
			_ = st.Close()
			_ = u.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := u.Read(make([]byte, 10)); !errors.Is(err, tc.want) {
				t.Fatalf("%v, want %v", err, tc.want)
			}
		})
	}
}

// Stream packets and datagrams both arrive, in any interleaving, each whole.
func TestUDPMixedPaths(t *testing.T) {
	for _, addressed := range []bool{false, true} {
		a, b := udpPair(t, addressed)
		const n, last = 200, 255 // last: the stream packet after all others
		mk := func(i int) []byte {
			p := bytes.Repeat([]byte{byte(i)}, 1+i*37%1400)
			if !addressed {
				return framed(p)
			}
			f, _ := socks5.AppendDatagram([]byte{0, 0}, netx.Addr{IP: netip.MustParseAddr("10.0.0.2"), Port: uint16(i + 1)}, p)
			binary.BigEndian.PutUint16(f, uint16(len(f)-2))
			return f
		}
		seen := make(map[int]bool)
		got := make(chan error, 1)
		go func() {
			buf := make([]byte, 2048)
			for {
				// After the last stream packet, late datagrams only.
				wait := 8 * time.Second
				if seen[last] {
					wait = 200 * time.Millisecond
				}
				_ = b.SetReadDeadline(time.Now().Add(wait))
				k, err := b.Read(buf)
				if err != nil {
					if seen[last] && isTimeout(err) {
						err = nil
					}
					got <- err
					return
				}
				f := buf[:k]
				i := int(f[len(f)-1])
				if addressed {
					if ad, _, err := socks5.DecodeDatagram(f[2:]); err != nil || int(ad.Port) != i+1 {
						got <- fmt.Errorf("addr %v %v", ad, err)
						return
					}
				}
				if seen[i] || !bytes.Equal(f, mk(i)) {
					got <- fmt.Errorf("frame %d", i)
					return
				}
				seen[i] = true
			}
		}()
		send := func(i int, dg bool) {
			a.wmu.Lock()
			a.datagrams = dg
			a.wmu.Unlock()
			if _, err := a.Write(mk(i)); err != nil {
				t.Fatal(err)
			}
		}
		for i := range n {
			send(i, i%2 == 0)
			if i%16 == 0 {
				time.Sleep(time.Millisecond)
			}
		}
		send(last, false)
		if err := <-got; err != nil {
			t.Fatal(err)
		}
		// Every stream packet arrives; a datagram may be lost, as zmux
		// counts.
		sa, sb := a.lc.Stats(), b.lc.Stats()
		lost := 0
		for i := range n {
			switch {
			case seen[i]:
			case i%2 == 1:
				t.Fatalf("stream packet %d lost", i)
			default:
				lost++
			}
		}
		if allowed := int(sb.DatagramsDroppedRecv) + int(sa.DatagramsSent-sb.DatagramsReceived); lost > allowed || lost == n/2 {
			t.Fatalf("%d datagrams lost, zmux lost %d", lost, allowed)
		}
	}
}

// Read deadlines interrupt a wait and leave the link usable.
func TestUDPReadDeadline(t *testing.T) { bothSessions(t, testUDPReadDeadline) }

func testUDPReadDeadline(t *testing.T, dg bool) {
	u, st, _ := rawUDPPeerOn(t, false, dg)
	_ = u.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := u.Read(make([]byte, 4)); !errors.Is(err, net.ErrClosed) && !isTimeout(err) {
		t.Fatalf("%v", err)
	}
	_ = u.SetReadDeadline(time.Time{})
	if _, err := st.Write([]byte{0x01, 'z'}); err != nil {
		t.Fatal(err)
	}
	if f := readFrame(t, u); string(f) != "z" {
		t.Fatalf("%q", f)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Close while reads and writes run returns them all; TestMain checks that
// no goroutine is left.
func TestUDPConcurrentClose(t *testing.T) { bothSessions(t, testUDPConcurrentClose) }

func testUDPConcurrentClose(t *testing.T, dg bool) {
	for range 20 {
		a, b := udpPairOn(t, false, dg)
		var wg sync.WaitGroup
		for _, c := range []*udpConn{a, b} {
			wg.Go(func() {
				buf := make([]byte, 2048)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			})
			wg.Go(func() {
				f := framed(make([]byte, 500))
				for {
					if _, err := c.Write(f); err != nil {
						return
					}
				}
			})
		}
		time.Sleep(5 * time.Millisecond)
		var cw sync.WaitGroup
		for _, c := range []*udpConn{a, b, a, b} {
			cw.Go(func() { _ = c.Close() })
		}
		cw.Wait()
		wg.Wait()
	}
}

func BenchmarkUDPConn(b *testing.B) {
	for _, bc := range []struct {
		name                 string
		addressed, datagrams bool
	}{
		{"fixed/stream", false, false},
		{"fixed/datagram", false, true},
		{"addressed/stream", true, false},
		{"addressed/datagram", true, true},
		{"fixed/stream-only-session", false, false},
		{"addressed/stream-only-session", true, false},
	} {
		b.Run(bc.name, func(b *testing.B) {
			x, y := udpPairOn(b, bc.addressed, !strings.HasSuffix(b.Name(), "-session"))
			x.datagrams = bc.datagrams
			p := bytes.Repeat([]byte{7}, 1200)
			f := framed(p)
			if bc.addressed {
				f, _ = socks5.AppendDatagram([]byte{0, 0}, netx.Addr{IP: netip.MustParseAddr("10.0.0.3"), Port: 53}, p)
				binary.BigEndian.PutUint16(f, uint16(len(f)-2))
			}
			buf := make([]byte, 65537)
			b.SetBytes(int64(len(f)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := x.Write(f); err != nil {
					b.Fatal(err)
				}
				if _, err := y.Read(buf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
