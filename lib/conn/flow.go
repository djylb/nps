package conn

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/djylb/nps/lib/file"
)

type LenConn struct {
	conn io.Writer
	Len  int
}

func NewLenConn(conn io.Writer) *LenConn {
	return &LenConn{conn: conn}
}

func (c *LenConn) Write(p []byte) (n int, err error) {
	n, err = c.conn.Write(p)
	c.Len += n
	return
}

type RWConn struct {
	io.ReadWriteCloser
	FakeAddr net.Addr
}

func NewRWConn(conn io.ReadWriteCloser) *RWConn {
	return &RWConn{
		ReadWriteCloser: conn,
		FakeAddr:        LocalTCPAddr,
	}
}

func (c *RWConn) LocalAddr() net.Addr                { return c.FakeAddr }
func (c *RWConn) RemoteAddr() net.Addr               { return c.FakeAddr }
func (c *RWConn) SetDeadline(_ time.Time) error      { return nil }
func (c *RWConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *RWConn) SetWriteDeadline(_ time.Time) error { return nil }

type FlowConn struct {
	*RWConn
	taskFlow   *file.Flow
	clientFlow *file.Flow
}

func NewFlowConn(conn io.ReadWriteCloser, task, client *file.Flow) *FlowConn {
	return &FlowConn{
		RWConn:     NewRWConn(conn),
		taskFlow:   task,
		clientFlow: client,
	}
}

func CheckFlowLimits(f *file.Flow, label string, now time.Time) error {
	if f.FlowLimit > 0 && (f.InletFlow+f.ExportFlow) > (f.FlowLimit<<20) {
		return fmt.Errorf("%s: flow limit exceeded", label)
	}
	if !f.TimeLimit.IsZero() && f.TimeLimit.Before(now) {
		return fmt.Errorf("%s: time limit exceeded", label)
	}
	return nil
}

func (c *FlowConn) Read(p []byte) (int, error) {
	n, err := c.RWConn.Read(p)
	n64 := int64(n)
	c.taskFlow.Add(0, n64)
	c.clientFlow.Add(n64, n64)
	now := time.Now()
	if err := CheckFlowLimits(c.taskFlow, "Task", now); err != nil {
		return n, err
	}
	if err := CheckFlowLimits(c.clientFlow, "Client", now); err != nil {
		return n, err
	}
	return n, err
}

func (c *FlowConn) Write(p []byte) (int, error) {
	n, err := c.RWConn.Write(p)
	n64 := int64(n)
	c.taskFlow.Add(n64, 0)
	c.clientFlow.Add(n64, n64)
	now := time.Now()
	if err := CheckFlowLimits(c.taskFlow, "Task", now); err != nil {
		return n, err
	}
	if err := CheckFlowLimits(c.clientFlow, "Client", now); err != nil {
		return n, err
	}
	return n, err
}
