package launch

import (
	"context"
	"sync"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/netaddr"
)

// Options holds the command line defaults a payload does not set.
type Options struct {
	DisconnectTimeout int
	P2PTimeout        int
}

// Run runs the profile until it stops or ctx ends. issued is the client key
// of the previous run, and the one to pass to the next is returned.
func (p Profile) Run(ctx context.Context, issued string, opts Options) (string, error) {
	c := p.Config.CommonConfig
	log.Info("launch profile starting", "name", p.Name, "server", c.Server, "carrier", c.Tp)
	if p.Direct {
		return issued, client.RunCoreProfile(ctx, c, false)
	}
	if c.DnsServer != "" {
		netaddr.SetCustomDNS(c.DnsServer)
	}
	if c.NtpServer != "" {
		clock.SetNtpServer(c.NtpServer)
	}
	if c.NtpInterval > 0 {
		clock.SetNtpInterval(time.Duration(c.NtpInterval) * time.Minute)
	}
	if len(p.Config.LocalServer) > 0 && c.DisconnectTime == 0 {
		c.DisconnectTime = opts.P2PTimeout
	}
	if p.Hosting {
		return issued, p.runHosting(ctx)
	}
	return client.RunConfigOnce(ctx, p.Config, issued)
}

// runHosting runs the ordinary client and the local accesses of p together,
// on the session they share, until either stops. The hosting side keeps
// the session up, so it is not disconnected while idle.
func (p Profile) runHosting(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := p.Config.CommonConfig
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	wg.Go(func() { errs <- client.RunCoreProfile(ctx, c, false) })
	wg.Go(func() {
		_, err := client.RunConfigOnce(ctx, p.Config, "")
		errs <- err
	})
	return <-errs
}
