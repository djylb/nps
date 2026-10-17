package conn

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/djylb/nps/lib/file"
)

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

// CheckFlowLimits returns an error naming label if f is over its traffic
// limit or past its time limit.
func CheckFlowLimits(f *file.Flow, label string) error {
	traffic, expired := f.Over()
	return flowLimitError(traffic, expired, label)
}

func flowLimitError(traffic, expired bool, label string) error {
	if traffic {
		return fmt.Errorf("%s: flow limit exceeded", label)
	}
	if expired {
		return fmt.Errorf("%s: time limit exceeded", label)
	}
	return nil
}

func (c *FlowConn) Read(p []byte) (int, error) {
	n, err := c.RWConn.Read(p)
	n64 := int64(n)
	return n, c.account(0, n64, n64, err)
}

func (c *FlowConn) Write(p []byte) (int, error) {
	n, err := c.RWConn.Write(p)
	n64 := int64(n)
	return n, c.account(n64, 0, n64, err)
}

// account adds in and out to the task flow and both to the client flow, and
// returns the first limit error, or else err.
func (c *FlowConn) account(in, out, both int64, err error) error {
	taskTraffic, taskExpired := c.taskFlow.AddOver(in, out)
	clientTraffic, clientExpired := c.clientFlow.AddOver(both, both)
	if lerr := flowLimitError(taskTraffic, taskExpired, "Task"); lerr != nil {
		return lerr
	}
	if lerr := flowLimitError(clientTraffic, clientExpired, "Client"); lerr != nil {
		return lerr
	}
	return err
}
