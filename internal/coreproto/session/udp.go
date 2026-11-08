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

// udpConn turns a UDP stream (§4.5) into the byte stream of length-prefixed
// frames the tunnel code reads and writes on every protocol
// (netx.FramedConn, one frame a datagram), SOCKS5 datagrams with their
// headers in the addressed mode. A write goes out as a datagram when one fits
// and as a stream packet otherwise; reads take both.
type udpConn struct {
	lc        *linkConn
	addressed bool
	zc        *zmux.Conn
	datagrams bool

	in     chan []byte // frames, header included
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once

	errMu sync.Mutex
	err   error // why the stream ended

	rbuf []byte // the rest of the frame being read
	rdl  deadline

	wmu   sync.Mutex
	wbuf  []byte
	spool []byte
}

func newUDPConn(lc *linkConn, addressed bool, zc *zmux.Conn) *udpConn {
	ctx, cancel := context.WithCancel(zc.Context())
	st := zc.ConnectionState()
	c := &udpConn{lc: lc, addressed: addressed, zc: zc, datagrams: st.SupportsDatagrams.Local && st.SupportsDatagrams.Remote,
		in: make(chan []byte, 64), ctx: ctx, cancel: cancel, rdl: makeDeadline()}
	c.wg.Go(c.pumpStream)
	if c.datagrams {
		c.wg.Go(c.pumpDatagrams)
	}
	return c
}

// frame is the view of one received body: a frame with its header, or nil
// for a packet that cannot be shown.
func (c *udpConn) frame(body []byte) []byte {
	if !c.addressed {
		f := make([]byte, udpFrameHeader, udpFrameHeader+len(body))
		binary.BigEndian.PutUint16(f, uint16(len(body)))
		return append(f, body...)
	}
	a, payload, err := wire.ParseUDPBody(body)
	if err != nil {
		log.Debug("udp packet dropped", "err", err)
		return nil
	}
	f, err := socks5.AppendDatagram(make([]byte, udpFrameHeader, udpFrameHeader+32+len(payload)), netx.Addr{IP: a.IP, Name: a.Host, Port: a.Port}, payload)
	if err != nil || len(f)-udpFrameHeader > netx.MaxFramePayload {
		return nil
	}
	binary.BigEndian.PutUint16(f, uint16(len(f)-udpFrameHeader))
	return f
}

func (c *udpConn) deliver(body []byte) bool {
	f := c.frame(body)
	if f == nil {
		return true
	}
	select {
	case c.in <- f:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *udpConn) pumpStream() {
	defer c.cancel()
	for {
		n, err := wire.ReadVarintFrom(c.lc)
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			c.setErr(err)
			return
		}
		if n > wire.MaxUDPPacket {
			c.setErr(wire.ErrMalformed)
			c.lc.abort(wire.CodeMalformed)
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.lc, body); err != nil {
			c.setErr(err)
			return
		}
		if !c.deliver(body) {
			return
		}
	}
}

func (c *udpConn) pumpDatagrams() {
	for {
		_, p, err := c.lc.ReceiveDatagram(c.ctx)
		if err != nil {
			return
		}
		if !c.deliver(p) {
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

func (c *udpConn) Read(p []byte) (int, error) {
	if len(c.rbuf) == 0 {
		select {
		case f := <-c.in:
			c.rbuf = f
		case <-c.rdl.wait():
			return 0, os.ErrDeadlineExceeded
		case <-c.ctx.Done():
			select {
			case f := <-c.in:
				c.rbuf = f
			default:
				c.errMu.Lock()
				err := c.err
				c.errMu.Unlock()
				if err == nil {
					err = io.EOF
				}
				return 0, err
			}
		}
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
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

// sendFrame sends the payload of one frame, the caller holding wmu.
func (c *udpConn) sendFrame(frame []byte) error {
	b := c.spool[:0]
	var err error
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
		b, err = wire.AppendUDPBody(b, &a, frame[n:])
		if err != nil {
			log.Debug("udp frame dropped", "err", err)
			return nil
		}
	} else {
		b = append(b, frame...)
	}
	c.spool = b
	if c.datagrams && len(b) <= c.lc.MaxDatagramPayload(0) {
		if err := c.lc.SendDatagram(0, b); err == nil {
			return nil
		}
	}
	head := wire.AppendVarint(c.spool[len(b):len(b)], uint64(len(b)))
	if _, err := c.lc.Write(head); err != nil {
		return err
	}
	_, err = c.lc.Write(b)
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

func (c *udpConn) SetDeadline(t time.Time) error {
	c.rdl.set(t)
	return c.lc.SetWriteDeadline(t)
}
func (c *udpConn) SetReadDeadline(t time.Time) error {
	c.rdl.set(t)
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
