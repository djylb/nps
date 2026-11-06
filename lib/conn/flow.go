package conn

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/djylb/byterate"
	"github.com/djylb/netx"
)

// Flow is a traffic account that connections count in and charge to. All its
// methods take a nil receiver of the implementing type, which accounts nothing.
type Flow interface {
	Budget() *byterate.Budget
	Traffic() *byterate.Counter
	// Over reports whether the account is over its traffic and time limits.
	Over() (traffic, expired bool)
}

func flowBudget(f Flow) *byterate.Budget {
	if f == nil {
		return nil
	}
	return f.Budget()
}

func flowTraffic(f Flow) *byterate.Counter {
	if f == nil {
		return nil
	}
	return f.Traffic()
}

// FlowConn counts the payload of a connection in the flows of its task and
// client and charges it to their budgets: reads count as outbound and writes
// as inbound traffic. A write never takes a flow past its traffic limit, a
// read or write past a limit fails with an error naming the flow, and the
// connection is closed once a flow's time limit passes.
type FlowConn struct {
	net.Conn
	taskFlow   Flow
	clientFlow Flow
	stops      [2]func() bool
}

// NewFlowConn wraps conn for the flows task and client, either of which may
// be nil. The addresses and deadlines are those of the connection beneath.
func NewFlowConn(conn io.ReadWriteCloser, task, client Flow) *FlowConn {
	budgets := byterate.NewHierarchy(flowBudget(task), flowBudget(client))
	opts := []byterate.Option{
		byterate.WithLimiters(budgets, budgets),
		byterate.WithCounter(flowTraffic(task)),
		byterate.WithCounter(flowTraffic(client)),
	}
	var c net.Conn
	if nc, ok := conn.(net.Conn); ok {
		c = byterate.NewConn(nc, opts...)
	} else {
		c = netx.WrapConn(byterate.NewStream(conn, opts...), netx.InnermostConn(conn))
	}
	fc := &FlowConn{Conn: c, taskFlow: task, clientFlow: client}
	for i, f := range []Flow{task, client} {
		budget := flowBudget(f)
		if budget == nil {
			continue
		}
		if _, expired := f.Over(); !expired {
			// An expired flow refuses the first read or write instead, with
			// an error that names it.
			fc.stops[i] = budget.AfterExpiry(func() { _ = c.Close() })
		}
	}
	return fc
}

// CheckFlowLimits returns an error naming label if f is over its traffic
// limit or past its time limit.
func CheckFlowLimits(f Flow, label string) error {
	if f == nil {
		return nil
	}
	traffic, expired := f.Over()
	if traffic {
		return fmt.Errorf("%s: flow limit exceeded", label)
	}
	if expired {
		return fmt.Errorf("%s: time limit exceeded", label)
	}
	return nil
}

func (c *FlowConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	return n, c.limitError(err)
}

func (c *FlowConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	return n, c.limitError(err)
}

// Close closes the connection and stops waiting for the time limits.
func (c *FlowConn) Close() error {
	for _, stop := range c.stops {
		if stop != nil {
			stop()
		}
	}
	return c.Conn.Close()
}

// limitError names the flow whose budget refused the bytes.
func (c *FlowConn) limitError(err error) error {
	if !errors.Is(err, byterate.ErrBudgetExhausted) {
		return err
	}
	if lerr := CheckFlowLimits(c.taskFlow, "Task"); lerr != nil {
		return lerr
	}
	if lerr := CheckFlowLimits(c.clientFlow, "Client"); lerr != nil {
		return lerr
	}
	return err
}
