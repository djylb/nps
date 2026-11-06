package conn

import (
	"errors"
	"io"

	"github.com/golang/snappy"
)

type SnappyConn struct {
	w *snappy.Writer
	r *snappy.Reader
	c io.Closer
}

func NewSnappyConn(conn io.ReadWriteCloser) *SnappyConn {
	c := new(SnappyConn)
	c.w = snappy.NewBufferedWriter(conn)
	c.r = snappy.NewReader(conn)
	c.c = conn.(io.Closer)
	return c
}

// snappy压缩写
func (s *SnappyConn) Write(b []byte) (n int, err error) {
	if n, err = s.w.Write(b); err != nil {
		return
	}
	if err = s.w.Flush(); err != nil {
		return
	}
	return
}

// snappy压缩读
func (s *SnappyConn) Read(b []byte) (n int, err error) {
	return s.r.Read(b)
}

func (s *SnappyConn) Close() error {
	err1 := s.w.Close()
	err2 := s.c.Close()
	if err1 != nil && err2 != nil {
		return errors.New(err1.Error() + err2.Error())
	}
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}
