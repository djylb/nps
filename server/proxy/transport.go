package proxy

import (
	"errors"
	"fmt"
	"net"

	"github.com/djylb/netx/transparent"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

// errNotRedirected is returned for a connection that reached the
// transparent proxy directly: its destination is the proxy itself.
var errNotRedirected = errors.New("connection was not redirected to the transparent proxy")

// HandleTrans forwards a connection redirected to the transparent proxy, by
// iptables REDIRECT or TPROXY on Linux or pf on macOS and FreeBSD, to its
// original destination.
func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	addr, err := originalTarget(c.Conn)
	if err != nil {
		logs.Warn("trans: client %d, task %d, remote %v: %v", s.Task.Client.Id, s.Task.Id, c.RemoteAddr(), err)
		return err
	}
	return s.DealClient(c, s.Task.Client, addr, nil, common.ConnTCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
}

// originalTarget returns the original destination of a redirected
// connection as host:port, with an IPv6 host in brackets. A connection that
// was not redirected is an error, since forwarding it would connect the
// proxy to itself.
func originalTarget(c net.Conn) (string, error) {
	dst, redirected, err := transparent.OriginalDestination(c)
	if err != nil {
		return "", err
	}
	if !redirected {
		return "", fmt.Errorf("%w: destination %v", errNotRedirected, dst)
	}
	return dst.String(), nil
}
