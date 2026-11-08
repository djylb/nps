package conn

import (
	"container/list"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const (
	splitIdle    = 2 * time.Minute // a source silent this long is forgotten
	splitMax     = 1 << 16         // sources remembered at most
	splitBacklog = 256             // packets queued per side
)

// UDPSplit shares one UDP socket between KCP (FEC 10/3 without encryption,
// as ListenKCP) and QUIC. The first packet of a source tells which: a KCP
// packet has the FEC type f1 or f2 at bytes 4 and 5, a QUIC long header
// does not (its bytes 1 to 4 are the version). The source is remembered
// until it has been silent for splitIdle. Both sides see plain
// net.PacketConns, so QUIC runs without GSO and batched reads here.
type UDPSplit struct {
	pc        net.PacketConn
	KCP, QUIC *splitSide
	mu        sync.Mutex
	src       map[netip.AddrPort]*list.Element
	order     list.List // of *splitSrc, least recently seen first
	closed    sync.Once
	done      chan struct{}
}

type splitSrc struct {
	ap   netip.AddrPort
	kcp  bool
	seen time.Time
}

// ListenUDPSplit listens on addr and starts splitting; Close ends it.
func ListenUDPSplit(addr string) (*UDPSplit, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	s := &UDPSplit{pc: pc, src: map[netip.AddrPort]*list.Element{}, done: make(chan struct{})}
	s.KCP, s.QUIC = newSplitSide(s), newSplitSide(s)
	go s.run()
	return s, nil
}

// Close closes the socket and both sides; the read loop has ended when the
// sides report errors.
func (s *UDPSplit) Close() error {
	var err error
	s.closed.Do(func() { err = s.pc.Close(); close(s.done) })
	return err
}

func (s *UDPSplit) run() {
	defer func() { _ = s.Close() }()
	buf := make([]byte, 65535)
	next := time.Now().Add(splitIdle)
	for {
		n, from, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		ap := from.(*net.UDPAddr).AddrPort()
		now := time.Now()
		s.mu.Lock()
		if now.After(next) {
			s.expire(now)
			next = now.Add(splitIdle)
		}
		kcp := s.classify(ap, n >= 6 && buf[5] == 0 && (buf[4] == 0xf1 || buf[4] == 0xf2), now)
		s.mu.Unlock()
		side := s.QUIC
		if kcp {
			side = s.KCP
		}
		select {
		case side.in <- splitPacket{append([]byte(nil), buf[:n]...), from}:
		default: // the side is behind: a lost packet
		}
	}
}

// expire forgets the sources silent for splitIdle; s.mu is held.
func (s *UDPSplit) expire(now time.Time) {
	for el := s.order.Front(); el != nil && now.Sub(el.Value.(*splitSrc).seen) > splitIdle; el = s.order.Front() {
		s.forget(el)
	}
}

func (s *UDPSplit) forget(el *list.Element) {
	delete(s.src, el.Value.(*splitSrc).ap)
	s.order.Remove(el)
}

// classify is whether ap is a KCP source: what it was first seen as, or kcp
// for a new one. A full table forgets its least recently seen source, in
// constant time, so a flood cannot lock new sources out; a source forgotten
// that way is told apart again by its next packet. s.mu is held.
func (s *UDPSplit) classify(ap netip.AddrPort, kcp bool, now time.Time) bool {
	if el := s.src[ap]; el != nil {
		e := el.Value.(*splitSrc)
		e.seen = now
		s.order.MoveToBack(el)
		return e.kcp
	}
	if len(s.src) >= splitMax {
		s.forget(s.order.Front())
	}
	s.src[ap] = s.order.PushBack(&splitSrc{ap: ap, kcp: kcp, seen: now})
	return kcp
}

type splitPacket struct {
	b    []byte
	from net.Addr
}

// splitSide is one protocol's view of the socket.
type splitSide struct {
	s    *UDPSplit
	in   chan splitPacket
	dl   chan struct{} // woken when the read deadline changes
	mu   sync.Mutex
	rdl  time.Time
	once sync.Once
	end  chan struct{}
}

func newSplitSide(s *UDPSplit) *splitSide {
	return &splitSide{s: s, in: make(chan splitPacket, splitBacklog), dl: make(chan struct{}, 1), end: make(chan struct{})}
}

func (c *splitSide) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		d := c.rdl
		c.mu.Unlock()
		var expire <-chan time.Time
		if !d.IsZero() {
			t := time.NewTimer(time.Until(d))
			defer t.Stop()
			expire = t.C
		}
		select {
		case pk := <-c.in:
			return copy(p, pk.b), pk.from, nil
		case <-expire:
			return 0, nil, os.ErrDeadlineExceeded
		case <-c.dl:
		case <-c.end:
			return 0, nil, net.ErrClosed
		case <-c.s.done:
			return 0, nil, net.ErrClosed
		}
	}
}

func (c *splitSide) WriteTo(p []byte, a net.Addr) (int, error) { return c.s.pc.WriteTo(p, a) }
func (c *splitSide) LocalAddr() net.Addr                       { return c.s.pc.LocalAddr() }
func (c *splitSide) SetDeadline(t time.Time) error             { return c.SetReadDeadline(t) }
func (c *splitSide) SetWriteDeadline(time.Time) error          { return nil }

func (c *splitSide) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rdl = t
	c.mu.Unlock()
	select {
	case c.dl <- struct{}{}:
	default:
	}
	return nil
}

// SetReadBuffer and SetWriteBuffer size the socket, as quic-go asks of a UDP conn.
func (c *splitSide) SetReadBuffer(n int) error  { return c.s.pc.(*net.UDPConn).SetReadBuffer(n) }
func (c *splitSide) SetWriteBuffer(n int) error { return c.s.pc.(*net.UDPConn).SetWriteBuffer(n) }

// Close ends this side's reads; the socket closes with the UDPSplit.
func (c *splitSide) Close() error { c.once.Do(func() { close(c.end) }); return nil }
