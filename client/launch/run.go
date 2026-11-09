package launch

import (
	"context"
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
	return client.RunConfigOnce(ctx, p.Config, issued)
}
