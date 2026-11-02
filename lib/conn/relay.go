package conn

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/djylb/byterate"
	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var (
	errFlowLimit = errors.New("flow limit exceeded")
	errTimeLimit = errors.New("time limit exceeded")
)

// Join relays between visitor and the connection to the client until either
// direction ends, then closes both. Each of flows counts the bytes from the
// visitor as inlet and those back as export traffic, and the relay stops
// once one of them is over its limits; the first bytes of each direction set
// task.IsHttp.
func Join(visitor, client net.Conn, flows []*file.Flow, task *file.Tunnel, remote string) {
	relay(client, visitor, flows, task, remote)
}

// relay copies between client, the connection to the client, and visitor
// until either direction ends, closes both, and returns the bytes written
// out to the visitor and in from it. See Join for flows, task and remote.
func relay(client, visitor io.ReadWriteCloser, flows []*file.Flow, task *file.Tunnel, remote string) (out, in int64) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var budgets []byterate.Limiter
	var counters []*byterate.Counter
	for _, fl := range flows {
		if fl == nil {
			continue
		}
		budgets = append(budgets, fl.Budget())
		counters = append(counters, fl.Traffic())
		if _, expired := fl.Over(); !expired {
			stop := fl.Budget().AfterExpiry(func() { cancel(errTimeLimit) })
			defer stop()
		}
	}
	limit := byterate.NewHierarchy(budgets...)
	out, in, _ = netx.Relay(ctx, client, visitor,
		netx.WithRelayFlows(relayFlow(limit, counters, flows, task, remote, false), relayFlow(limit, counters, flows, task, remote, true)))
	return out, in
}

// relayFlow returns the RelayFlow of a direction of a relay, in from the
// visitor or out to it: Wait lets bytes pass up to the traffic limits of
// the flows, Done counts them in their counters and budgets, and First
// tells whether the direction carries HTTP.
func relayFlow(limit byterate.Limiter, counters []*byterate.Counter, flows []*file.Flow, task *file.Tunnel, remote string, inbound bool) *netx.RelayFlow {
	f := new(netx.RelayFlow)
	if limit != nil {
		f.Wait = func(ctx context.Context, n int64) (int64, error) {
			n, err := limit.Ready(ctx, n)
			return n, flowsError(flows, err)
		}
		f.Done = func(n int64) error {
			// A counter's reads are the outbound and its writes the inbound
			// traffic, as for a FlowConn wrapping the connection to the client.
			for _, c := range counters {
				if inbound {
					c.Add(0, n)
				} else {
					c.Add(n, 0)
				}
			}
			return flowsError(flows, limit.Charge(n))
		}
	}
	if task != nil {
		f.First = func(b []byte) { detectHTTP(b, task, remote) }
	}
	return f
}

// flowsError turns a budget's refusal into the error of the limit of flows
// that was reached.
func flowsError(flows []*file.Flow, err error) error {
	if !errors.Is(err, byterate.ErrBudgetExhausted) {
		return err
	}
	for _, fl := range flows {
		traffic, expired := fl.Over()
		if traffic {
			logs.Info("Flow limit exceeded")
			return errFlowLimit
		}
		if expired {
			logs.Info("Time limit exceeded")
			return errTimeLimit
		}
	}
	return err
}

// httpPrefixes are the first three bytes of the HTTP requests and responses
// that detectHTTP recognises.
var httpPrefixes = [...]string{"HTT", "GET", "POS", "HEA", "PUT", "DEL"}

// detectHTTP sets task.IsHttp from the first bytes b of a direction, which
// are left as they are when 3 bytes or fewer, and logs the request line and
// the line after it of a request.
func detectHTTP(b []byte, task *file.Tunnel, remote string) {
	if len(b) > 4096 {
		b = b[:4096]
	}
	if len(b) <= 3 {
		return
	}
	isHTTP := false
	for _, p := range httpPrefixes {
		if string(b[:3]) == p {
			isHTTP = true
			break
		}
	}
	if isHTTP && string(b[:3]) != "HTT" {
		first, rest, ok := strings.Cut(string(b), "\r\n")
		if ok {
			second, _, _ := strings.Cut(rest, "\r\n")
			target := ""
			if task.Target != nil {
				target = task.Target.TargetStr
			}
			logs.Info("HTTP Request method %s, %s, remote address %s, target %s", first, second, remote, target)
		}
	}
	task.Lock()
	task.IsHttp = isHTTP
	task.Unlock()
}
