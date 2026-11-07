package bridge

import (
	"context"
	"errors"
	"net"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
)

// TunnelDialer is a running tunnel that bridge:// targets of its id reach.
type TunnelDialer interface {
	DialVirtual(remote string) (net.Conn, error)
	ServeVirtual(c net.Conn)
}

// TunnelLookup finds the running tunnel of an id.
type TunnelLookup func(id int) (TunnelDialer, bool)

// SetTunnelLookup sets how the tunnels of tunnel:// targets are found.
func (s *Bridge) SetTunnelLookup(fn TunnelLookup) { s.lookup.Store(&fn) }

// SetWebListener sets the in-memory listener of the web manager that
// bridge://web targets reach; nil takes it away.
func (s *Bridge) SetWebListener(l *netx.ChanListener) { s.webListener.Store(l) }

// SetIPRegisterListener sets the in-memory listener of the registration page
// that bridge://ipreg targets reach; nil takes it away.
func (s *Bridge) SetIPRegisterListener(l *netx.ChanListener) { s.ipRegListener.Store(l) }

// TunnelConn connects to the running tunnel id as remote.
func (s *Bridge) TunnelConn(id int, remote string) (net.Conn, error) {
	fn := s.lookup.Load()
	if fn == nil || *fn == nil {
		return nil, errors.New("tunnel lookup not set")
	}
	d, ok := (*fn)(id)
	if !ok || d == nil {
		return nil, errors.New("tunnel not found")
	}
	return d.DialVirtual(remote)
}

// IPRegisterConn connects a bridge://ipreg target to the registration page,
// with remote, the visitor, as the address it sees.
func (s *Bridge) IPRegisterConn(ctx context.Context, remote string) (net.Conn, error) {
	l := s.ipRegListener.Load()
	if l == nil {
		return nil, errors.New("the IP registration page is not running")
	}
	return conn.DialVirtual(ctx, l, remote)
}

// WebServerConn connects to the web server's in-memory listener as remote.
func (s *Bridge) WebServerConn(ctx context.Context, remote string) (net.Conn, error) {
	l := s.webListener.Load()
	if l == nil {
		return nil, errors.New("web server not set")
	}
	return conn.DialVirtual(ctx, l, remote)
}
