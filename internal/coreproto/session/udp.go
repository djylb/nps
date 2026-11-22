package session

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// udpFrameHeader is the length prefix of the frames udpConn shows.
const udpFrameHeader = 2

// udpHeadroom is the room kept before a body built for the stream, for its
// Len varint (at most 8 bytes), so that head and body leave in one write.
const udpHeadroom = 8

// udpStreamBuf is the first size of the buffer stream packets are read
// into; it grows to the largest packet seen, at most MaxUDPPacket.
const udpStreamBuf = 2048

// udpSocksHeadMax bounds the SOCKS5 datagram header a frame shows: RSV RSV
// FRAG, ATYP, a domain of 255 bytes with its length, the port.
const udpSocksHeadMax = 3 + 1 + 1 + 255 + 2

// udpConn turns a UDP stream (§4.5) into the byte stream of length-prefixed
// frames the tunnel code reads and writes on every protocol
// (netx.FramedConn, one frame a datagram), SOCKS5 datagrams with their
// headers in the addressed mode. A write goes out as a datagram when one fits
// and as a stream packet otherwise; reads take both.
//
// Read parses stream packets itself, on its caller's goroutine: with a
// blocking read of the stream when datagrams were not negotiated, otherwise
// from what the stream has (TryRead, NotifyReadable while it waits), the
// datagrams having a goroutine of their own, zmux offering no wait for both
// at once.
type udpConn struct {
	rdl    deadline
	ctx    context.Context
	err    error         // why the stream ended; under errMu
	notify func()        // sends to wake, for NotifyReadable
	wake   chan struct{} // the stream became readable: the token of the fired call
	lc     *linkConn
	zc     *zmux.Conn
	cancel context.CancelFunc
	dgrams chan []byte // datagram bodies; nil without datagrams
	rbuf   []byte      // the rest of the frame being read (in fbuf); under rmu
	spool  []byte      // under wmu
	wbuf   []byte      // under wmu
	sbuf   []byte      // stream bytes read: sbuf[soff:] is not parsed yet; under rmu
	fbuf   []byte      // a frame shown in pieces; under rmu
	wg     sync.WaitGroup
	soff   int // under rmu
	once   sync.Once

	rmu   sync.Mutex // Read, and what follows under it: rbuf, fbuf, sbuf, soff, ended, noted, turn, rdl
	errMu sync.Mutex // err
	wmu   sync.Mutex // wbuf, spool

	ended     bool // the stream ended, err set; under rmu
	noted     bool // a NotifyReadable call is pending or its token in wake; under rmu
	turn      bool // which source Read tries first; under rmu
	addressed bool
	datagrams bool
}

func newUDPConn(lc *linkConn, addressed bool, zc *zmux.Conn) *udpConn {
	ctx, cancel := context.WithCancel(zc.Context())
	st := zc.ConnectionState()
	c := &udpConn{lc: lc, addressed: addressed, zc: zc, datagrams: st.SupportsDatagrams.Local && st.SupportsDatagrams.Remote,
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, rdl: makeDeadline()}
	c.notify = func() {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
	if c.datagrams {
		c.dgrams = make(chan []byte, 64)
		c.wg.Go(c.pumpDatagrams)
	}
	return c
}

// show puts the frame of one received body in p, or in fbuf what does not
// fit; ok false for a packet that cannot be shown.
func (c *udpConn) show(p, body []byte) (int, bool) {
	if !c.addressed {
		if len(body) > wire.MaxUDPPayload {
			return 0, false
		}
		if len(p) >= udpFrameHeader+len(body) {
			binary.BigEndian.PutUint16(p, uint16(len(body)))
			return udpFrameHeader + copy(p[udpFrameHeader:], body), true
		}
		c.fbuf = binary.BigEndian.AppendUint16(c.fbuf[:0], uint16(len(body)))
		c.fbuf = append(c.fbuf, body...)
		return c.pieces(p), true
	}
	a, payload, err := wire.ParseUDPBody(body)
	if err != nil {
		log.Debug("udp packet dropped", "err", err)
		return 0, false
	}
	direct := len(p) >= udpFrameHeader+udpSocksHeadMax+len(payload)
	dst := c.fbuf[:0]
	if direct {
		dst = p[:0:len(p)]
	}
	f, err := socks5.AppendDatagram(append(dst, 0, 0), netx.Addr{IP: a.IP, Name: a.Host, Port: a.Port}, payload)
	if err != nil || len(f)-udpFrameHeader > netx.MaxFramePayload {
		return 0, false
	}
	binary.BigEndian.PutUint16(f, uint16(len(f)-udpFrameHeader))
	if direct {
		return len(f), true
	}
	c.fbuf = f
	return c.pieces(p), true
}

// pieces starts showing the frame in fbuf.
func (c *udpConn) pieces(p []byte) int {
	n := copy(p, c.fbuf)
	c.rbuf = c.fbuf[n:]
	return n
}

// oversized reports a packet whose Payload is over the limit of §4.5.
func (c *udpConn) oversized(body []byte) bool {
	if !c.addressed {
		return len(body) > wire.MaxUDPPayload
	}
	_, payload, err := wire.ParseUDPBody(body)
	return err == nil && len(payload) > wire.MaxUDPPayload
}

// end records the end of the stream, which ends the datagrams too.
func (c *udpConn) end(err error) {
	c.setErr(err)
	c.ended = true
	c.cancel()
}

func (c *udpConn) malformed() {
	c.lc.abort(wire.CodeMalformed)
	c.end(wire.ErrMalformed)
}

// pollStream shows the next stream packet the stream has now, or, with
// block, waits for one; err is a read deadline that passed.
func (c *udpConn) pollStream(p []byte, block bool) (int, bool, error) {
	for !c.ended {
		b := c.sbuf[c.soff:]
		need := 1
		if len(b) > 0 {
			need = 1 << (b[0] >> 6)
		}
		if len(b) >= need {
			n, m, _ := wire.ReadVarint(b)
			if n > wire.MaxUDPPacket {
				c.malformed()
				return 0, false, nil
			}
			need = m + int(n)
			if len(b) >= need {
				body := b[m:need]
				c.soff += need
				if c.oversized(body) {
					c.malformed()
					return 0, false, nil
				}
				if k, ok := c.show(p, body); ok {
					return k, true, nil
				}
				continue
			}
		}
		if ok, err := c.fill(need, block); !ok {
			return 0, false, err
		}
	}
	return 0, false, nil
}

// fill reads what the stream has now (with block, waits for some) after the
// unparsed bytes, with room for need of them; false when nothing came, err
// a read deadline that passed.
func (c *udpConn) fill(need int, block bool) (bool, error) {
	if c.soff > 0 {
		c.sbuf = c.sbuf[:copy(c.sbuf, c.sbuf[c.soff:])]
		c.soff = 0
	}
	if cap(c.sbuf) < need {
		c.sbuf = append(make([]byte, 0, max(need, udpStreamBuf)), c.sbuf...)
	}
	var n int
	var err error
	if block {
		n, err = c.lc.Read(c.sbuf[len(c.sbuf):cap(c.sbuf)])
	} else {
		n, err = c.lc.TryRead(c.sbuf[len(c.sbuf):cap(c.sbuf)])
	}
	c.sbuf = c.sbuf[:len(c.sbuf)+n]
	switch {
	case n > 0:
		return true, nil
	case errors.Is(err, zmux.ErrWouldBlock):
		return false, nil
	case errors.Is(err, os.ErrDeadlineExceeded):
		return false, err
	case errors.Is(err, io.EOF):
		// The stream ended inside a packet's body: unexpected; at a
		// boundary, inside Len or before the body: the end.
		if b := c.sbuf; len(b) > 0 && len(b) > 1<<(b[0]>>6) {
			err = io.ErrUnexpectedEOF
		}
	case err == nil:
		return false, nil // an empty read: wait again
	}
	c.end(err)
	return false, nil
}

// pollDatagram shows the next queued datagram.
func (c *udpConn) pollDatagram(p []byte) (int, bool) {
	for {
		select {
		case d := <-c.dgrams:
			if n, ok := c.show(p, d); ok {
				return n, true
			}
		default:
			return 0, false
		}
	}
}

// poll shows a packet that is there now, from both sources in turn.
func (c *udpConn) poll(p []byte) (int, bool) {
	c.turn = !c.turn
	if c.turn {
		if n, ok := c.pollDatagram(p); ok {
			return n, true
		}
		n, ok, _ := c.pollStream(p, false)
		return n, ok
	}
	if n, ok, _ := c.pollStream(p, false); ok {
		return n, true
	}
	return c.pollDatagram(p)
}

func (c *udpConn) pumpDatagrams() {
	for {
		_, p, err := c.lc.ReceiveDatagram(c.ctx)
		if err != nil {
			return
		}
		select {
		case c.dgrams <- p:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *udpConn) setErr(err error) {
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
}

func (c *udpConn) endErr() error {
	c.errMu.Lock()
	err := c.err
	c.errMu.Unlock()
	if err == nil {
		err = io.EOF
	}
	return err
}

func (c *udpConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if len(c.rbuf) > 0 {
			n := copy(p, c.rbuf)
			c.rbuf = c.rbuf[n:]
			return n, nil
		}
		if c.dgrams == nil {
			// The stream alone: its own blocking read, its own deadline.
			if c.ended {
				return 0, c.endErr()
			}
			if n, ok, err := c.pollStream(p, true); ok || err != nil {
				return n, err
			}
			continue
		}
		if n, ok := c.poll(p); ok {
			return n, nil
		}
		if c.ended {
			// The datagrams queued before the end are still shown.
			if n, ok := c.pollDatagram(p); ok {
				return n, nil
			}
			return 0, c.endErr()
		}
		if !c.noted {
			// Kept from one wait to the next until it fires, the
			// stream's call costs nothing while datagrams flow.
			c.noted = true
			c.lc.NotifyReadable(c.notify)
		}
		select {
		case d := <-c.dgrams:
			if n, ok := c.show(p, d); ok {
				return n, nil
			}
		case <-c.wake:
			c.noted = false
		case <-c.rdl.wait():
			return 0, os.ErrDeadlineExceeded
		case <-c.ctx.Done():
			if n, ok := c.poll(p); ok {
				return n, nil
			}
			if !c.ended {
				c.end(nil) // closed, or the session ended
			}
		}
	}
}

func (c *udpConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if len(c.wbuf) == 0 && len(p) >= udpFrameHeader && int(binary.BigEndian.Uint16(p))+udpFrameHeader == len(p) {
		// One whole frame, which is what framed writers produce.
		if err := c.sendFrame(p[udpFrameHeader:]); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	c.wbuf = append(c.wbuf, p...)
	for len(c.wbuf) >= udpFrameHeader {
		n := int(binary.BigEndian.Uint16(c.wbuf)) + udpFrameHeader
		if len(c.wbuf) < n {
			break
		}
		if err := c.sendFrame(c.wbuf[udpFrameHeader:n]); err != nil {
			return 0, err
		}
		c.wbuf = c.wbuf[:copy(c.wbuf, c.wbuf[n:])]
	}
	return len(p), nil
}

// sendFrame sends the payload of one frame, the caller holding wmu. A body
// built here starts udpHeadroom into spool, for the Len of a stream packet.
func (c *udpConn) sendFrame(frame []byte) error {
	if cap(c.spool) < udpHeadroom {
		c.spool = make([]byte, udpHeadroom, udpHeadroom+udpStreamBuf)
	}
	body := frame
	if c.addressed {
		addr, n, derr := socks5.DecodeDatagram(frame)
		if derr != nil {
			log.Debug("udp frame dropped", "err", derr)
			return nil
		}
		a := wire.Address{IP: addr.IP, Host: addr.Name, Port: addr.Port}
		if a.IP.IsValid() {
			a.IP = addrUnmapped(a.IP)
			a.Host = ""
		}
		b, err := wire.AppendUDPBody(c.spool[:udpHeadroom], &a, frame[n:])
		if err != nil {
			log.Debug("udp frame dropped", "err", err)
			return nil
		}
		c.spool = b
		body = b[udpHeadroom:]
	}
	if c.datagrams && len(body) <= c.lc.MaxDatagramPayload(0) {
		if err := c.lc.SendDatagram(0, body); err == nil {
			return nil
		}
	}
	if !c.addressed {
		c.spool = append(c.spool[:udpHeadroom], frame...)
		body = c.spool[udpHeadroom:]
	}
	start := udpHeadroom - wire.VarintLen(uint64(len(body)))
	wire.AppendVarint(c.spool[start:start], uint64(len(body)))
	_, err := c.lc.Write(c.spool[start : udpHeadroom+len(body)])
	return err
}

func addrUnmapped(a netip.Addr) netip.Addr { return a.Unmap() }

func (c *udpConn) Close() error {
	c.once.Do(func() {
		_ = c.lc.Close()
		c.cancel()
	})
	c.wg.Wait()
	return nil
}

func (c *udpConn) CloseWrite() error    { return c.lc.CloseWrite() }
func (c *udpConn) LocalAddr() net.Addr  { return c.lc.LocalAddr() }
func (c *udpConn) RemoteAddr() net.Addr { return c.lc.RemoteAddr() }

// SetDeadline sets the read deadline, which is udpConn's own while Read waits
// for two sources and the stream's when it reads the stream alone, and the
// write deadline of the stream.
func (c *udpConn) SetDeadline(t time.Time) error {
	c.rdl.set(t)
	if c.dgrams == nil {
		return c.lc.SetDeadline(t)
	}
	return c.lc.SetWriteDeadline(t)
}

func (c *udpConn) SetReadDeadline(t time.Time) error {
	c.rdl.set(t)
	if c.dgrams == nil {
		return c.lc.SetReadDeadline(t)
	}
	return nil
}
func (c *udpConn) SetWriteDeadline(t time.Time) error { return c.lc.SetWriteDeadline(t) }

// deadline is a read deadline as a channel, as net.Pipe keeps it.
type deadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{}
}

func makeDeadline() deadline { return deadline{cancel: make(chan struct{})} }

func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // the timer's function has run
	}
	d.timer = nil
	closed := false
	select {
	case <-d.cancel:
		closed = true
	default:
	}
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		ch := d.cancel
		d.timer = time.AfterFunc(dur, func() { close(ch) })
		return
	}
	if !closed {
		close(d.cancel)
	}
}

func (d *deadline) wait() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}
