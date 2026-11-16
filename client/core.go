package client

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
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
	e.ServeUDP5Policy(ctx, c, nil)
}

// ServeUDP5Policy is ServeUDP5 under a policy for each datagram.
func (e coreExit) ServeUDP5Policy(ctx context.Context, c net.Conn, allow conn.UDP5Policy) {
	localIP := ""
	if LocalIPForward {
		localIP = e.localIP
	}
	conn.HandleUdp5Policy(ctx, c, e.udpIdle, localIP, allow)
}

// plainHint keeps the hint about a silent plain carrier to one per process.
var plainHint sync.Once

// connectFailure names the class of a failed connect for the reason field of
// connect_failed and gives a hint for it. Only classes the error types tell
// apart are named: a wrong key gets no reply, as a silent server does, so it
// stays establish.
func connectFailure(err error) (reason, hint string) {
	_, alert := errors.AsType[tls.AlertError](err)
	_, header := errors.AsType[tls.RecordHeaderError](err)
	_, dns := errors.AsType[*net.DNSError](err)
	op, dial := errors.AsType[*net.OpError](err)
	switch {
	case errors.Is(err, coreapi.ErrAuthClosed):
		return "auth", "the server refused the client: check the vkey and that the client is enabled"
	case alert || header:
		return "tls", "the tls handshake failed: check that the server address, port and carrier (type) match a tls port of the server"
	case dns:
		return "dns", "the server host name did not resolve: check the server address and the dns settings"
	case dial && op.Op == "dial":
		return "dial", "the server could not be reached: check the server address, the port and the firewall"
	}
	return "establish", ""
}

// runCore is RunCore with after, which runs on each session once it is up
// (the config upload). A session after fails ends, and the error is returned
// when no reconnect follows. shared is the entry of a session others may be
// using, nil for a session of this run alone: the entry's users decide when
// a shared one closes. The sessions report into the Reconnect of ctx, which
// paces the reconnects.
func runCore(ctx context.Context, d coreapi.Dialer, autoReconnect bool, after func(context.Context, coreapi.ClientSession) error, shared *coreEntry) error {
	r := reconnectOf(ctx)
	attempt := 0
	for ctx.Err() == nil {
		var failed error
		s, err := d.Dial(ctx)
		if err != nil {
			attempt++
			if errors.Is(err, coreapi.ErrPlainRefused) {
				plainHint.Do(func() {
					log.Warn("the server did not answer on the plain tcp carrier; if it takes only tls, use -type=tls or a launch link with type=tls (or auto)", "server", d.Server())
				})
			}
			reason, hint := connectFailure(err)
			args := []any{"server", d.Server(), "attempt", attempt, "reason", reason}
			if hint != "" {
				args = append(args, "hint", hint)
			}
			slogx.Emit(ctx, log, &logevent.ConnectFailed, append(args, "err", err)...)
		} else {
			attempt = 0
			r.up()
			NowStatus.Store(1)
			slogx.Emit(ctx, log, &logevent.Connected, "server", d.Server(), "carrier", s.Carrier())
			if after != nil {
				if failed = after(ctx, s); failed != nil {
					if shared != nil && !shared.sole() {
						// The others keep the session; only this run ends.
						if !autoReconnect || !r.Wait(ctx) {
							return failed
						}
						continue
					}
					_ = s.Close()
				}
			}
			select {
			case <-s.Done():
				NowStatus.Store(0)
				r.ended(s.Err())
				args := []any{"server", d.Server(), "carrier", s.Carrier(), "reason", "lost"}
				if errors.Is(s.Err(), coreapi.ErrAuthClosed) {
					_, hint := connectFailure(s.Err())
					args = append(args, "hint", hint)
				}
				slogx.Emit(ctx, log, &logevent.Disconnected, append(args, "err", s.Err())...)
			case <-ctx.Done():
				if shared != nil {
					return nil
				}
				_ = s.Close()
				<-s.Done()
				return nil
			}
		}
		if !autoReconnect || !r.Wait(ctx) {
			return failed
		}
	}
	return nil
}
