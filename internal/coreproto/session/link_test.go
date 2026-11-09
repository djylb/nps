package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/zstd"
	"github.com/zmuxio/zmux-go"
)

// plainExit dials directly and relays SOCKS5 datagrams with the frames of the
// tunnel code.
type plainExit struct{}

func (plainExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, target)
}

func (plainExit) ServeUDP5(ctx context.Context, c net.Conn) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	relay := socks5.DatagramRelay{IdleTimeout: 5 * time.Second}
	_ = relay.Relay(ctx, netx.NewFramedConn(c, netx.WithFramedDatagrams()), socks5.NewPacketConn(pc))
}

// linkEnv is a session with an exit.
type linkEnv struct {
	e      *env
	server *serverSession
	client *clientSession
}

func startLink(t *testing.T, cfg DialConfig) *linkEnv {
	t.Helper()
	e := start(t, "tcp", coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: true, Crypt: true})
	cfg.Candidates, cfg.VKey, cfg.Agent, cfg.Timeout = e.cands(e.addr), "key-1", "t", 3*time.Second
	if cfg.Exit == nil {
		cfg.Exit = plainExit{}
	}
	s, err := NewDialer(cfg).Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(); <-s.Done() })
	return &linkEnv{e: e, server: <-e.online, client: s.(*clientSession)}
}

func listenEcho(t *testing.T) string { return listenBanner(t, "") }

// listenBanner is listenEcho for a server-first protocol: it writes banner on
// accept, before it reads anything.
func listenBanner(t *testing.T, banner string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = c.Close() }()
				if banner != "" {
					_, _ = io.WriteString(c, banner)
				}
				_, _ = io.Copy(c, c)
			})
		}
	})
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	return ln.Addr().String()
}

func listenUDPEcho(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		b := make([]byte, 65535)
		for {
			n, a, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(b[:n], a)
		}
	})
	t.Cleanup(func() { _ = pc.Close(); wg.Wait() })
	return pc.LocalAddr().String()
}

func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

func bg(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func roundTrip(t *testing.T, c net.Conn, msg []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("echo differs")
	}
}

func TestTCPLinkEcho(t *testing.T) {
	l := startLink(t, DialConfig{})
	for _, reply := range []bool{false, true} {
		c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: listenEcho(t), Source: "1.2.3.4:5", WantReply: reply})
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, c, []byte("hello"))
		_ = c.Close()
	}
}

func TestDialFailureCodes(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := closedPort(t)
	_, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: target, WantReply: true})
	var le *coreapi.LinkError
	if !errors.As(err, &le) || le.Code != wire.CodeConnectionRefused || le.NetReason() != "refused" {
		t.Fatalf("want_reply: %v", err)
	}
	c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Read(make([]byte, 1))
	var se *zmux.StreamError
	if !errors.As(err, &se) || uint64(se.ErrorCode) != wire.CodeConnectionRefused {
		t.Fatalf("no reply: %v", err)
	}
}

func TestDialErrorMapping(t *testing.T) {
	for msg, code := range map[string]uint64{
		"dial tcp: connect: connection refused": wire.CodeConnectionRefused,
		"connect: network is unreachable":       wire.CodeNetworkUnreachable,
		"connect: no route to host":             wire.CodeHostUnreachable,
		"something else":                        wire.CodeGeneralFailure,
	} {
		if got := dialError(errors.New(msg)); got != code {
			t.Errorf("%q: %#x, want %#x", msg, got, code)
		}
	}
	if got := dialError(&net.DNSError{Err: "x", Name: "y"}); got != wire.CodeDNSFailure {
		t.Errorf("dns: %#x", got)
	}
	if got := dialError(context.DeadlineExceeded); got != wire.CodeConnectTimeout {
		t.Errorf("timeout: %#x", got)
	}
}

func udpExchange(t *testing.T, f *netx.FramedConn, msg []byte, check func([]byte)) {
	t.Helper()
	_ = f.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := f.Write(msg); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 65535)
	n, err := f.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	check(b[:n])
}

func datagramsSent(c net.Conn) uint64 { return c.(*udpConn).lc.Stats().DatagramsSent }

func TestUDPFixedDatagramAndStream(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenUDPEcho(t)
	for _, datagrams := range []bool{true, false} {
		c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkUDP, Target: target, WantReply: true})
		if err != nil {
			t.Fatal(err)
		}
		if !c.(*udpConn).datagrams {
			t.Fatal("datagrams were not negotiated")
		}
		c.(*udpConn).datagrams = datagrams
		f := netx.NewFramedConn(c, netx.WithFramedDatagrams())
		msg := []byte("ping-udp")
		udpExchange(t, f, msg, func(b []byte) {
			if !bytes.Equal(b, msg) {
				t.Fatalf("echo %q", b)
			}
		})
		if got := datagramsSent(c); (got > 0) != datagrams {
			t.Fatalf("datagrams=%v: sent %d", datagrams, got)
		}
		_ = f.Close()
	}
}

func TestUDPAddressed(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenUDPEcho(t)
	ap, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, datagrams := range []bool{true, false} {
		c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkUDP})
		if err != nil {
			t.Fatal(err)
		}
		c.(*udpConn).datagrams = datagrams
		f := netx.NewFramedConn(c, netx.WithFramedDatagrams())
		dst := netx.Addr{IP: ap.AddrPort().Addr().Unmap(), Port: ap.AddrPort().Port()}
		msg, err := socks5.AppendDatagram(nil, dst, []byte("addressed"))
		if err != nil {
			t.Fatal(err)
		}
		udpExchange(t, f, msg, func(b []byte) {
			a, n, err := socks5.DecodeDatagram(b)
			if err != nil || a.Port != dst.Port || string(b[n:]) != "addressed" {
				t.Fatalf("reply %q %v %v", b, a, err)
			}
		})
		_ = f.Close()
	}
}

// udpConn takes frames written in pieces as framed writers may.
func TestUDPFrameAcrossWrites(t *testing.T) {
	l := startLink(t, DialConfig{})
	c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkUDP, Target: listenUDPEcho(t), WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	frame := binary.BigEndian.AppendUint16(nil, 4)
	frame = append(frame, "abcd"...)
	for _, b := range [][]byte{frame[:1], frame[1:3], frame[3:]} {
		if _, err := c.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	got := make([]byte, 6)
	_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, frame) {
		t.Fatalf("%q %v", got, err)
	}
}

var algs = []string{"flate", "zstd", "snappy"}

func TestCompressedLinkFlushAndHalfClose(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenEcho(t)
	for _, alg := range algs {
		t.Run(alg, func(t *testing.T) {
			c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: target, Compress: []string{alg}, WantReply: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			if c.(*compressConn).alg != compressNames[alg] {
				t.Fatal("algorithm not used")
			}
			// Each message comes back with the stream still open: flushed.
			for range 3 {
				msg := bytes.Repeat([]byte("compress me "), 2000)
				roundTrip(t, c, msg)
			}
			// Half-close: the end of this direction, the echo still flows.
			if _, err := c.Write([]byte("tail")); err != nil {
				t.Fatal(err)
			}
			if err := c.(*compressConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			b, err := io.ReadAll(c)
			if err != nil || string(b) != "tail" {
				t.Fatalf("%q %v", b, err)
			}
		})
	}
}

func TestCompressedEmptyStream(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenEcho(t)
	for _, alg := range algs {
		t.Run(alg, func(t *testing.T) {
			c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: target, Compress: []string{alg}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			if err := c.(*compressConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			if b, err := io.ReadAll(c); err != nil || len(b) != 0 {
				t.Fatalf("%q %v", b, err)
			}
		})
	}
}

func TestCompressNegotiationPicksPeerAlgorithm(t *testing.T) {
	// Not advertised means not compressed, with no substitute (§7.2).
	if got := chooseCompress([]string{"snappy"}, []uint64{wire.CompressFlate}); got != 0 {
		t.Fatalf("old compress on: %d", got)
	}
	if got := chooseCompress([]string{"snappy"}, []uint64{wire.CompressFlate, wire.CompressSnappy}); got != wire.CompressSnappy {
		t.Fatalf("advertised: %d", got)
	}
	if got := chooseCompress([]string{"zstd"}, []uint64{wire.CompressFlate}); got != 0 {
		t.Fatalf("configured but unsupported: %d", got)
	}
}

// rawOpen opens a compressed TCP stream and writes body as it is.
func rawOpen(t *testing.T, l *linkEnv, op wire.Op, compress uint64, target string, body []byte) *zmux.Stream {
	t.Helper()
	a, err := wire.ParseHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := wire.AppendStreamOpen(&wire.StreamOpen{Op: op, Target: &a})
	if err != nil {
		t.Fatal(err)
	}
	if compress != 0 {
		// after target, in id order
		info = wire.AppendUint(info, wire.KeyCompress, compress)
	}
	st, err := l.server.zc.OpenStreamSync(bg(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreamble(info); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(body) > 0 {
		if _, err := st.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	return st
}

func wantReset(t *testing.T, st *zmux.Stream, code uint64) {
	t.Helper()
	_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := io.ReadAll(st)
	var se *zmux.StreamError
	if !errors.As(err, &se) || uint64(se.ErrorCode) != code {
		t.Fatalf("got %v, want code %#x", err, code)
	}
}

func deflated(b []byte, finish bool) []byte {
	var o bytes.Buffer
	w, _ := flate.NewWriter(&o, 1)
	_, _ = w.Write(b)
	if finish {
		_ = w.Close()
	} else {
		_ = w.Flush()
	}
	return o.Bytes()
}

func TestCompressTruncationAndGarbage(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenEcho(t)
	zw, _ := zstd.NewWriter(nil)
	frame := zw.EncodeAll([]byte("hello hello hello"), nil)
	_ = zw.Close()
	for name, c := range map[string]struct {
		alg  uint64
		body []byte
	}{
		"flate no final block": {wire.CompressFlate, deflated([]byte("abc"), false)},
		"flate empty":          {wire.CompressFlate, nil},
		"flate trailing":       {wire.CompressFlate, append(deflated([]byte("abc"), true), 1, 2, 3)},
		"zstd cut":             {wire.CompressZstd, frame[:len(frame)-2]},
		"zstd no frame":        {wire.CompressZstd, nil},
		"snappy garbage":       {wire.CompressSnappy, []byte("not a snappy stream")},
	} {
		t.Run(name, func(t *testing.T) {
			wantReset(t, rawOpen(t, l, wire.OpTCP, c.alg, target, c.body), wire.CodeCompressError)
		})
	}
	// The session stays.
	if l.server.Closed() {
		t.Fatal("session closed")
	}
}

func TestCompressRatioLimit(t *testing.T) {
	l := startLink(t, DialConfig{CompressLimits: CompressLimits{MaxRatio: 1}})
	wantReset(t, rawOpen(t, l, wire.OpTCP, wire.CompressFlate, listenEcho(t), deflated(make([]byte, 20<<20), true)), wire.CodeCompressError)
}

func TestCompressWindowLimit(t *testing.T) {
	l := startLink(t, DialConfig{CompressLimits: CompressLimits{MaxWindow: 1 << 16}})
	var o bytes.Buffer
	zw, _ := zstd.NewWriter(&o, zstd.WithWindowSize(1<<22), zstd.WithSingleSegment(false))
	buf := make([]byte, 1<<20)
	_, _ = rand.Read(buf)
	_, _ = zw.Write(buf)
	_ = zw.Close()
	wantReset(t, rawOpen(t, l, wire.OpTCP, wire.CompressZstd, listenEcho(t), o.Bytes()), wire.CodeCompressError)
}

func TestRefusedOpens(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenEcho(t)
	wantReset(t, rawOpen(t, l, wire.OpTCP, 99, target, nil), wire.CodeUnsupported)
	wantReset(t, rawOpen(t, l, wire.OpUDP, wire.CompressFlate, target, nil), wire.CodeMalformed)
}

func TestClientOpenedStreamsNotAllowed(t *testing.T) {
	l := startLink(t, DialConfig{})
	a, _ := wire.ParseHostPort("127.0.0.1:80")
	for name, o := range map[string]wire.StreamOpen{
		"tcp":        {Op: wire.OpTCP, Target: &a},
		"udp":        {Op: wire.OpUDP, Target: &a},
		"tcp linkid": {Op: wire.OpTCP, Target: &a, LinkID: 77},
	} {
		info, err := wire.AppendStreamOpen(&o)
		if err != nil {
			t.Fatal(err)
		}
		st, err := l.client.zc.OpenStreamSync(bg(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetPreamble(info); err != nil {
			t.Fatal(err)
		}
		_ = st.Flush()
		t.Run(name, func(t *testing.T) { wantReset(t, st, wire.CodeNotAllowed) })
	}
}

func applied(l *linkEnv) chan wire.Snapshot {
	ch := make(chan wire.Snapshot, 16)
	l.client.mu.Lock()
	l.client.onApply = func(_ uint64, s wire.Snapshot) { ch <- s }
	l.client.mu.Unlock()
	return ch
}

func waitSnapshot(t *testing.T, ch chan wire.Snapshot, ok func(wire.Snapshot) bool) {
	t.Helper()
	end := time.After(4 * time.Second)
	for {
		select {
		case s := <-ch:
			if ok(s) {
				return
			}
		case <-end:
			t.Fatal("no such snapshot")
		}
	}
}

func TestConfigFollowsChanges(t *testing.T) {
	l := startLink(t, DialConfig{})
	ch := applied(l)
	l.e.a.ClientChanged(coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: true, Crypt: true, Compress: []string{"zstd"}})
	waitSnapshot(t, ch, func(s wire.Snapshot) bool { return s.Compress == wire.CompressZstd && s.Record == wire.RecordDefault })
	l.e.a.ClientChanged(coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: true, Crypt: false, Compress: []string{"zstd"}})
	waitSnapshot(t, ch, func(s wire.Snapshot) bool { return s.Record == wire.RecordTrusted })
}

func TestConfigCarriesSchedulerUp(t *testing.T) {
	l := startLink(t, DialConfig{})
	ch := applied(l)
	rec := coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: true, Crypt: true}
	rec.SchedulerUp = "spread_eta"
	l.e.a.ClientChanged(rec)
	waitSnapshot(t, ch, func(s wire.Snapshot) bool { return s.HasSchedulerUp && s.SchedulerUp == wire.SchedulerSpreadETA })
	// Back to unset: the next snapshot tells npc nothing.
	rec.SchedulerUp = ""
	l.e.a.ClientChanged(rec)
	waitSnapshot(t, ch, func(s wire.Snapshot) bool { return !s.HasSchedulerUp })
}

func TestConfigSeqAndFailureKeepOld(t *testing.T) {
	l := startLink(t, DialConfig{})
	deadline := time.Now().Add(3 * time.Second)
	base := uint64(0)
	for base == 0 && time.Now().Before(deadline) {
		l.client.mu.Lock()
		base = l.client.applied
		l.client.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	if base == 0 {
		t.Fatal("first snapshot not applied")
	}
	l.client.apply(base+5, wire.Snapshot{Record: wire.RecordSealed})
	l.client.apply(base+1, wire.Snapshot{Record: wire.RecordTrusted})
	l.client.d.mu.Lock()
	r := l.client.d.record
	l.client.d.mu.Unlock()
	if r != wire.RecordSealed {
		t.Fatalf("stale snapshot applied: %d", r)
	}
	st, err := l.server.zc.OpenUniStream()
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(wire.AppendConfigOpen(base + 9))
	_, _ = st.Write([]byte{0xff})
	_ = st.Close()
	time.Sleep(200 * time.Millisecond)
	l.client.d.mu.Lock()
	r = l.client.d.record
	l.client.d.mu.Unlock()
	if r != wire.RecordSealed {
		t.Fatalf("malformed snapshot applied: %d", r)
	}
}

func TestDisabledClientClosedWithAuth(t *testing.T) {
	l := startLink(t, DialConfig{})
	l.e.a.ClientChanged(coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: false, Crypt: true})
	select {
	case <-l.client.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("session not closed")
	}
	var ae *zmux.ApplicationError
	if !errors.As(l.client.Err(), &ae) || uint64(ae.ErrorCode) != wire.CodeAuth {
		t.Fatalf("%v", l.client.Err())
	}
}
