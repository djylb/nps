package rate

import (
	"io"
)

type rateConn struct {
	conn io.ReadWriteCloser
	rate *Rate
}

func NewRateConn(conn io.ReadWriteCloser, rate *Rate) io.ReadWriteCloser {
	return &rateConn{
		conn: conn,
		rate: rate,
	}
}

func (s *rateConn) Read(b []byte) (n int, err error) {
	n, err = s.conn.Read(b)
	if s.rate != nil && n > 0 {
		s.rate.Get(int64(n))
	}
	return
}

func (s *rateConn) Write(b []byte) (n int, err error) {
	if s.rate != nil && len(b) > 0 {
		s.rate.Get(int64(len(b)))
	}
	n, err = s.conn.Write(b)
	if s.rate != nil && len(b) > 0 && n < len(b) {
		s.rate.ReturnBucket(int64(len(b) - n))
	}
	return
}

func (s *rateConn) Close() error {
	return s.conn.Close()
}
