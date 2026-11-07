package bridge

import (
	"context"
	"errors"
	"net"
	"sync/atomic"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
)

// TunnelDialer is a running tunnel that bridge:// targets of its id reach.
type TunnelDialer interface {
	DialVirtual(remote string) (net.Conn, error)
	ServeVirtual(c net.Conn)
}

var lookup atomic.Value // holds: func(int) (TunnelDialer, bool)

// SetTunnelLookup sets how the tunnels of bridge:// targets are found.
func SetTunnelLookup(fn func(int) (TunnelDialer, bool)) {
	lookup.Store(fn)
}

func GetTunnelConn(id int, remote string) (net.Conn, error) {
	v := lookup.Load()
	if v == nil {
		return nil, errors.New("tunnel lookup not set")
	}
	fn := v.(func(int) (TunnelDialer, bool))
	d, ok := fn(id)
	if !ok || d == nil {
		return nil, errors.New("tunnel not found")
	}
	return d.DialVirtual(remote)
}

// WebServerListener feeds the web manager; see GetWebServerConn.
var WebServerListener *netx.ChanListener

// IPRegisterListener feeds the registration page; see GetIPRegisterConn.
var IPRegisterListener *netx.ChanListener

// GetIPRegisterConn connects a bridge://ipreg target to the registration
// page, with remote, the visitor, as the address it sees.
func GetIPRegisterConn(ctx context.Context, remote string) (net.Conn, error) {
	if IPRegisterListener == nil {
		return nil, errors.New("the IP registration page is not running")
	}
	return conn.DialVirtual(ctx, IPRegisterListener, remote)
}

// GetWebServerConn connects to the web server's in-memory listener as remote.
func GetWebServerConn(ctx context.Context, remote string) (net.Conn, error) {
	if WebServerListener == nil {
		return nil, errors.New("web server not set")
	}
	return conn.DialVirtual(ctx, WebServerListener, remote)
}
