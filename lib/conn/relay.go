package conn

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var (
	errFlowLimit = errors.New("flow limit exceeded")
	errTimeLimit = errors.New("time limit exceeded")
)

// Join relays between c1 and c2 until either direction ends, then closes
// both. Every byte written either way is added as both inlet and export
// traffic to each of flows, and the relay stops once one of them is over its
// limits; the first bytes of each direction set task.IsHttp.
func Join(c1, c2 net.Conn, flows []*file.Flow, task *file.Tunnel, remote string) {
	relay(c1, c2, flows, task, remote)
}

// relay copies between a and b until either direction ends, closes both, and
// returns the bytes written from a to b and from b to a. See Join for flows,
// task and remote.
func relay(a, b io.ReadWriteCloser, flows []*file.Flow, task *file.Tunnel, remote string) (aToB, bToA int64) {
	f := relayFlow(flows, task, remote)
	aToB, bToA, _ = netx.Relay(context.Background(), a, b, netx.WithRelayFlows(f, f))
	return aToB, bToA
}

// relayFlow returns the RelayFlow of either direction of a relay: Done
// counts the bytes written to each flow and ends the relay at a limit, First
// tells whether the direction carries HTTP. Both are shared by the two
// directions and safe for concurrent use.
func relayFlow(flows []*file.Flow, task *file.Tunnel, remote string) netx.RelayFlow {
	var f netx.RelayFlow
	if len(flows) > 0 {
		f.Done = func(n int64) error {
			for _, fl := range flows {
				if fl == nil {
					continue
				}
				traffic, expired := fl.AddOver(n, n)
				if traffic {
					logs.Info("Flow limit exceeded")
					return errFlowLimit
				}
				if expired {
					logs.Info("Time limit exceeded")
					return errTimeLimit
				}
			}
			return nil
		}
	}
	if task != nil {
		f.First = func(b []byte) { detectHTTP(b, task, remote) }
	}
	return f
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
