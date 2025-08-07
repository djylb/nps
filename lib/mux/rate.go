package mux

import (
	"net"
	"sync/atomic"
	"time"
)

type Rate struct {
	bucketSize        int64
	bucketSurplusSize int64
	bucketAddSize     int64
	stopChan          chan struct{}
	NowRate           int64
}

func NewRate(addSize int64) *Rate {
	return &Rate{
		bucketSize:        addSize * 2,
		bucketSurplusSize: 0,
		bucketAddSize:     addSize,
		stopChan:          make(chan struct{}),
	}
}

func (s *Rate) Start() {
	go s.session()
}

func (s *Rate) add(size int64) {
	if res := s.bucketSize - s.bucketSurplusSize; res < s.bucketAddSize {
		atomic.AddInt64(&s.bucketSurplusSize, res)
		return
	}
	atomic.AddInt64(&s.bucketSurplusSize, size)
}

func (s *Rate) ReturnBucket(size int64) {
	s.add(size)
}

func (s *Rate) Stop() {
	close(s.stopChan)
}

func (s *Rate) Get(size int64) {
	if s.bucketSurplusSize >= size {
		atomic.AddInt64(&s.bucketSurplusSize, -size)
		return
	}
	ticker := time.NewTicker(time.Millisecond * 100)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if s.bucketSurplusSize >= size {
				atomic.AddInt64(&s.bucketSurplusSize, -size)
				return
			}
		case <-s.stopChan:
			return
		}
	}
}

func (s *Rate) session() {
	ticker := time.NewTicker(time.Second * 1)
	for {
		select {
		case <-ticker.C:
			if rs := s.bucketAddSize - s.bucketSurplusSize; rs > 0 {
				s.NowRate = rs
			} else {
				s.NowRate = s.bucketSize - s.bucketSurplusSize
			}
			s.add(s.bucketAddSize)
		case <-s.stopChan:
			ticker.Stop()
			return
		}
	}
}

type RateConn struct {
	conn net.Conn
	rate *Rate
}

func NewRateConn(rate *Rate, conn net.Conn) *RateConn {
	return &RateConn{
		conn: conn,
		rate: rate,
	}
}

func (conn *RateConn) Read(b []byte) (n int, err error) {
	defer func() {
		conn.rate.Get(int64(n))
	}()
	return conn.conn.Read(b)
}

func (conn *RateConn) Write(b []byte) (n int, err error) {
	defer func() {
		conn.rate.Get(int64(n))
	}()
	return conn.conn.Write(b)
}

func (conn *RateConn) LocalAddr() net.Addr {
	return conn.conn.LocalAddr()
}

func (conn *RateConn) RemoteAddr() net.Addr {
	return conn.conn.RemoteAddr()
}

func (conn *RateConn) SetDeadline(t time.Time) error {
	return conn.conn.SetDeadline(t)
}

func (conn *RateConn) SetWriteDeadline(t time.Time) error {
	return conn.conn.SetWriteDeadline(t)
}

func (conn *RateConn) SetReadDeadline(t time.Time) error {
	return conn.conn.SetReadDeadline(t)
}

func (conn *RateConn) Close() error {
	return conn.conn.Close()
}
