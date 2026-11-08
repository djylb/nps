package client

import (
	"context"
	"net"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// coreExit serves the links the server opens on a core protocol session.
type coreExit struct {
	localIP string
	udpIdle time.Duration
}

// NewCoreExit returns the exit of npc on the core protocol; it dials targets
// as the old protocol's client does. udpIdle is the exit's own backstop for a
// SOCKS5 UDP association without traffic, the entry's timeout being the one
// that normally ends it; zero or negative is DefaultUDP5ExitIdle.
func NewCoreExit(localIP string, udpIdle time.Duration) coreapi.Exit {
	if udpIdle <= 0 {
		udpIdle = DefaultUDP5ExitIdle
	}
	return coreExit{localIP: localIP, udpIdle: udpIdle}
}

// DefaultUDP5ExitIdle is the exit's SOCKS5 UDP idle backstop.
const DefaultUDP5ExitIdle = 300 * time.Second

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
	conn.HandleUdp5(ctx, c, e.udpIdle, localIP)
}

// RunCore keeps a core protocol session of one server profile until ctx ends
// (or, without autoReconnect, until the first one ends). The tunnels are run
// by the session, through the Exit of the dialer.
func RunCore(ctx context.Context, d coreapi.Dialer, autoReconnect bool) {
	_ = runCore(ctx, d, autoReconnect, nil)
}

// runCore is RunCore with after, which runs on each session once it is up
// (the config upload). A session after fails ends, and the error is returned
// when no reconnect follows.
func runCore(ctx context.Context, d coreapi.Dialer, autoReconnect bool, after func(context.Context, coreapi.ClientSession) error) error {
	attempt := 0
	for ctx.Err() == nil {
		var failed error
		s, err := d.Dial(ctx)
		if err != nil {
			attempt++
			slogx.Emit(ctx, log, &logevent.ConnectFailed, "server", d.Server(), "attempt", attempt, "reason", "establish", "err", err)
		} else {
			attempt = 0
			slogx.Emit(ctx, log, &logevent.Connected, "server", d.Server(), "carrier", s.Carrier())
			if after != nil {
				if failed = after(ctx, s); failed != nil {
					_ = s.Close()
				}
			}
			select {
			case <-s.Done():
				slogx.Emit(ctx, log, &logevent.Disconnected, "server", d.Server(), "carrier", s.Carrier(), "reason", "lost", "err", s.Err())
			case <-ctx.Done():
				_ = s.Close()
				<-s.Done()
				return nil
			}
		}
		if !autoReconnect || !WaitReconnect(ctx) {
			return failed
		}
	}
	return nil
}
