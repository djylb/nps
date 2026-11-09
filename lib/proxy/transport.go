package proxy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"

	"github.com/djylb/netx/transparent"
	"github.com/djylb/nps/lib/conn"
)

// errNotRedirected is returned for a connection that reached the
// transparent proxy directly: its destination is the proxy itself.
var errNotRedirected = errors.New("connection was not redirected to the transparent proxy")

// listenTransparent opens the listener of a transparent proxy mode. TPROXY
// delivers connections only to a socket with the transparent option, which
// takes elevated privileges. REDIRECT and pf rdr work without it, so an
// unprivileged process falls back to a plain listener.
func listenTransparent(address string) (net.Listener, error) {
	l, err := transparent.Listen(context.Background(), "tcp", address)
	if errors.Is(err, fs.ErrPermission) {
		log.Warn("transparent sockets unavailable", "listen", address, "reason", "permission", "detail", "TPROXY rules will not reach the listener", "err", err)
		return net.Listen("tcp", address)
	}
	return l, err
}

// HandleTrans forwards a connection redirected to the transparent proxy, by
// iptables REDIRECT or TPROXY on Linux or pf on macOS and FreeBSD, to its
// original destination.
func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	addr, err := originalTarget(c.Conn)
	if err != nil {
		log.Debug("transparent target unknown", "client", s.Task.Client.Id, "tunnel", s.Task.Id, "src_ip", srcIP(c.RemoteAddr()), "err", err)
		return err
	}
	target := s.Task.View().Target
	return s.DealClient(c, s.Task.Client, addr, nil, conn.LinkTCP, nil, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, target.ProxyProtocol, target.LocalProxy, s.Task)
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
