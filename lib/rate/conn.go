package rate

import (
	"io"
	"sync"
)

type rateConn struct {
	conn      io.ReadWriteCloser
	rate      *Rate
	done      chan struct{}
	closeOnce sync.Once
}

func NewRateConn(conn io.ReadWriteCloser, rate *Rate) io.ReadWriteCloser {
	return &rateConn{
		conn: conn,
		rate: rate,
		done: make(chan struct{}),
	}
}

func (s *rateConn) Read(b []byte) (n int, err error) {
	n, err = s.conn.Read(b)
	if s.rate != nil && n > 0 {
		s.rate.Wait(int64(n), s.done)
	}
	return
}

func (s *rateConn) Write(b []byte) (n int, err error) {
	if s.rate != nil && len(b) > 0 {
		s.rate.Wait(int64(len(b)), s.done)
	}
	n, err = s.conn.Write(b)
	if s.rate != nil && len(b) > 0 && n < len(b) {
		s.rate.ReturnBucket(int64(len(b) - n))
	}
	return
}

// Close also wakes any Read or Write waiting for rate budget.
func (s *rateConn) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return s.conn.Close()
}
