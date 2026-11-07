package conn

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/nps/lib/netaddr"
)

// smartUdpReadErrorEnqueueTimeout bounds how long a read loop waits to queue
// its final error when the packet queue is full.
const smartUdpReadErrorEnqueueTimeout = 250 * time.Millisecond

type packet struct {
	buf  []byte
	n    int
	addr net.Addr
	err  error
}

type SmartUdpConn struct {
	conns     []net.PacketConn
	fakeLocal *net.UDPAddr
	packetCh  chan packet
	quit      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex
	lastConn  net.PacketConn
}

func NewSmartUdpConn(conns []net.PacketConn, addr *net.UDPAddr) *SmartUdpConn {
	s := &SmartUdpConn{
		conns:     conns,
		fakeLocal: addr,
		packetCh:  make(chan packet, 1024),
		quit:      make(chan struct{}),
	}
	s.wg.Add(len(conns))
	for _, c := range conns {
		go s.readLoop(c)
	}
	return s
}

func (s *SmartUdpConn) readLoop(c net.PacketConn) {
	defer s.wg.Done()
	for {
		buf := BufPool.Get()
		n, addr, err := c.ReadFrom(buf)
		s.mu.Lock()
		s.lastConn = c
		s.mu.Unlock()
		pkt := packet{buf: buf, n: n, addr: addr, err: err}

		select {
		case <-s.quit:
			BufPool.Put(buf)
			return
		case s.packetCh <- pkt:
			// delivered, buffer will be returned in ReadFrom or on flush
		default:
			if err == nil || !s.enqueueReadError(pkt) {
				BufPool.Put(buf)
			}
		}

		if err != nil {
			return
		}
	}
}

// enqueueReadError waits briefly for room for the read error that ends a
// read loop. Data packets are dropped when the queue is full, but the error
// must reach ReadFrom, otherwise a consumer of a dead socket never learns it.
func (s *SmartUdpConn) enqueueReadError(pkt packet) bool {
	t := time.NewTimer(smartUdpReadErrorEnqueueTimeout)
	defer t.Stop()
	select {
	case s.packetCh <- pkt:
		return true
	case <-s.quit:
	case <-t.C:
	}
	return false
}

func (s *SmartUdpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	pkt, ok := <-s.packetCh
	if !ok {
		return 0, nil, io.EOF
	}
	defer BufPool.Put(pkt.buf)
	if pkt.err != nil {
		return 0, nil, pkt.err
	}
	n := copy(p, pkt.buf[:pkt.n])
	return n, pkt.addr, nil
}

func (s *SmartUdpConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("unsupported addr type %T", addr)
	}
	want4 := udpAddr.IP.To4() != nil
	for _, c := range s.conns {
		la := c.LocalAddr().(*net.UDPAddr)
		is4 := la.IP == nil || la.IP.To4() != nil
		if is4 == want4 {
			s.mu.Lock()
			s.lastConn = c
			s.mu.Unlock()
			return c.WriteTo(p, addr)
		}
	}
	s.mu.Lock()
	s.lastConn = s.conns[0]
	s.mu.Unlock()
	return s.conns[0].WriteTo(p, addr)
}

func (s *SmartUdpConn) UDPConns() []*net.UDPConn {
	out := make([]*net.UDPConn, 0, len(s.conns))
	for _, c := range s.conns {
		if uc, ok := c.(*net.UDPConn); ok && uc != nil {
			out = append(out, uc)
		}
	}
	return out
}

func (s *SmartUdpConn) Close() error {
	s.closeOnce.Do(func() {
		close(s.quit)
		for _, c := range s.conns {
			if err := c.Close(); err != nil {
				s.closeErr = err
			}
		}
		s.wg.Wait()
		close(s.packetCh)
		for pkt := range s.packetCh {
			BufPool.Put(pkt.buf)
		}
	})
	return s.closeErr
}

func (s *SmartUdpConn) LocalAddr() net.Addr {
	s.mu.Lock()
	c := s.lastConn
	s.mu.Unlock()
	if c != nil {
		return c.LocalAddr()
	}
	return s.fakeLocal
}

func (s *SmartUdpConn) SetDeadline(t time.Time) error {
	for _, c := range s.conns {
		_ = c.SetDeadline(t)
	}
	return nil
}

func (s *SmartUdpConn) SetReadDeadline(t time.Time) error {
	for _, c := range s.conns {
		_ = c.SetReadDeadline(t)
	}
	return nil
}

func (s *SmartUdpConn) SetWriteDeadline(t time.Time) error {
	for _, c := range s.conns {
		_ = c.SetWriteDeadline(t)
	}
	return nil
}

func resolveUDPBindTarget(addr string) (*net.UDPAddr, string, string, bool, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, "", "", false, err
	}
	host := netaddr.GetIpByAddr(addr)
	if host == "" {
		return udpAddr, "", "", false, nil
	}
	ip := netaddr.NormalizeIP(udpAddr.IP)
	if ip == nil {
		ip = netaddr.NormalizeIP(net.ParseIP(host))
	}
	if ip == nil {
		return nil, "", "", false, fmt.Errorf("invalid udp bind ip %q", host)
	}
	network := "udp6"
	if ip.To4() != nil {
		network = "udp4"
	}
	return udpAddr, network, net.JoinHostPort(ip.String(), strconv.Itoa(udpAddr.Port)), true, nil
}
