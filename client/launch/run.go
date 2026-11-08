package launch

import (
	"context"
	"sync"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/netaddr"
)

// Options holds the command line defaults a payload does not set.
type Options struct {
	DisconnectTimeout int
	P2PTimeout        int
}

// Run runs the profiles until the first one stops or ctx ends, then stops the
// others and waits for them. uuids keeps each profile's client uuid across
// runs. It returns the error of the profile that stopped first, if any.
func Run(ctx context.Context, profiles []Profile, uuids map[string]string, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		done  bool
	)
	for _, p := range profiles {
		uuid := uuids[p.Name]
		wg.Go(func() {
			uuid, err := p.run(ctx, uuid, opts)
			mu.Lock()
			defer mu.Unlock()
			uuids[p.Name] = uuid
			if !done {
				first, done = err, true
			}
			cancel()
		})
	}
	wg.Wait()
	return first
}

func (p Profile) run(ctx context.Context, uuid string, opts Options) (string, error) {
	c := p.Config.CommonConfig
	log.Info("launch profile starting", "name", p.Name, "server", c.Server, "carrier", c.Tp)
	if p.Direct {
		disconnect := c.DisconnectTime
		if disconnect == 0 {
			disconnect = opts.DisconnectTimeout
		}
		client.NewRPClient(c.Server, c.VKey, c.Tp, c.ProxyUrl, c.LocalIP, "", nil, disconnect, nil).Start(ctx)
		return uuid, nil
	}
	if c.DnsServer != "" {
		netaddr.SetCustomDNS(c.DnsServer)
	}
	if c.NtpServer != "" {
		clock.SetNtpServer(c.NtpServer)
	}
	if len(p.Config.LocalServer) > 0 && c.DisconnectTime == 0 {
		c.DisconnectTime = opts.P2PTimeout
	}
	return client.RunConfigOnce(ctx, p.Config, uuid)
}
