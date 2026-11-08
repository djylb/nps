package conn

import (
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
	src       map[netip.AddrPort]*splitSrc
	closed    sync.Once
	done      chan struct{}
}

type splitSrc struct {
	kcp  bool
	seen time.Time
}

// ListenUDPSplit listens on addr and starts splitting; Close ends it.
func ListenUDPSplit(addr string) (*UDPSplit, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	s := &UDPSplit{pc: pc, src: map[netip.AddrPort]*splitSrc{}, done: make(chan struct{})}
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
			for k, v := range s.src {
				if now.Sub(v.seen) > splitIdle {
					delete(s.src, k)
				}
			}
			next = now.Add(splitIdle)
		}
		e := s.src[ap]
		if e == nil && len(s.src) < splitMax {
			e = &splitSrc{kcp: n >= 6 && buf[5] == 0 && (buf[4] == 0xf1 || buf[4] == 0xf2)}
			s.src[ap] = e
		}
		if e != nil {
			e.seen = now
		}
		s.mu.Unlock()
		if e == nil {
			continue
		}
		side := s.QUIC
		if e.kcp {
			side = s.KCP
		}
		select {
		case side.in <- splitPacket{append([]byte(nil), buf[:n]...), from}:
		default: // the side is behind: a lost packet
		}
	}
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
