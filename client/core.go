package client

import (
	"context"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// RunCore keeps a core protocol session of one server profile until ctx ends
// (or, without autoReconnect, until the first one ends). The session carries
// nothing yet: tunnels arrive in step 2 of the switch.
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
