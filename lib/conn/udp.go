package conn

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
)

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
		buf := common.BufPoolMax.Get().([]byte)
		n, addr, err := c.ReadFrom(buf)
		s.mu.Lock()
		s.lastConn = c
		s.mu.Unlock()
		pkt := packet{buf: buf, n: n, addr: addr, err: err}

		select {
		case <-s.quit:
			common.PutBufPoolMax(buf)
			return
		case s.packetCh <- pkt:
			// delivered, buffer will be returned in ReadFrom or on flush
		default:
			common.PutBufPoolMax(buf)
		}

		if err != nil {
			return
		}
	}
}

func (s *SmartUdpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	pkt, ok := <-s.packetCh
	if !ok {
		return 0, nil, io.EOF
	}
	defer common.PutBufPoolMax(pkt.buf)
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
			common.PutBufPoolMax(pkt.buf)
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

func NewUdpConnByAddr(addr string) (net.PacketConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	port := common.GetPortStrByAddr(addr)

	var conns []net.PacketConn
	if pc4, e4 := net.ListenPacket("udp4", ":"+port); e4 == nil {
		conns = append(conns, pc4)
	}
	if pc6, e6 := net.ListenPacket("udp6", ":"+port); e6 == nil {
		conns = append(conns, pc6)
	}
	if len(conns) == 1 {
		return conns[0], nil
	}
	if len(conns) > 1 {
		return NewSmartUdpConn(conns, udpAddr), nil
	}
	return net.ListenPacket("udp", addr)
}
