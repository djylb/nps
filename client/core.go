package client

import (
	"context"
	"net"

	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// coreExit serves the links the server opens on a core protocol session.
type coreExit struct{ localIP string }

// NewCoreExit returns the exit of npc on the core protocol; it dials targets
// as the old protocol's client does.
func NewCoreExit(localIP string) coreapi.Exit { return coreExit{localIP: localIP} }

// Dial implements coreapi.Exit. The context carries the timeout.
func (e coreExit) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	return dialExit(ctx, network, target, e.localIP, 0)
}

// ServeUDP5 implements coreapi.Exit.
func (e coreExit) ServeUDP5(ctx context.Context, c net.Conn) {
	localIP := ""
	if LocalIPForward {
		localIP = e.localIP
	}
	conn.HandleUdp5(ctx, c, socks5.DefaultUDPIdleTimeout, localIP)
}

// RunCore keeps a core protocol session of one server profile until ctx ends
// (or, without autoReconnect, until the first one ends). The tunnels are run
// by the session, through the Exit of the dialer.
func RunCore(ctx context.Context, d coreapi.Dialer, autoReconnect bool) {
	attempt := 0
	for ctx.Err() == nil {
		s, err := d.Dial(ctx)
		if err != nil {
			attempt++
			slogx.Emit(ctx, log, &logevent.ConnectFailed, "server", d.Server(), "attempt", attempt, "reason", "establish", "err", err)
		} else {
			attempt = 0
			slogx.Emit(ctx, log, &logevent.Connected, "server", d.Server(), "carrier", s.Carrier())
			select {
			case <-s.Done():
				slogx.Emit(ctx, log, &logevent.Disconnected, "server", d.Server(), "carrier", s.Carrier(), "reason", "lost", "err", s.Err())
			case <-ctx.Done():
				_ = s.Close()
				<-s.Done()
				return
			}
		}
		if !autoReconnect || !WaitReconnect(ctx) {
			return
		}
	}
}
