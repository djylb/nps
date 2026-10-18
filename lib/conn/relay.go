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
	out, in, _ = netx.Relay(context.Background(), client, visitor,
		netx.WithRelayFlows(relayFlow(flows, task, remote, false), relayFlow(flows, task, remote, true)))
	return out, in
}

// relayFlow returns the RelayFlow of a direction of a relay, in from the
// visitor or out to it: Done counts the bytes written to each flow and ends
// the relay at a limit, First tells whether the direction carries HTTP.
func relayFlow(flows []*file.Flow, task *file.Tunnel, remote string, inbound bool) netx.RelayFlow {
	var f netx.RelayFlow
	if len(flows) > 0 {
		f.Done = func(n int64) error {
			in, out := int64(0), n
			if inbound {
				in, out = n, 0
			}
			for _, fl := range flows {
				if fl == nil {
					continue
				}
				traffic, expired := fl.AddOver(in, out)
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
