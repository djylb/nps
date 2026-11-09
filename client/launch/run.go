package launch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/netaddr"
)

// RelaunchWait is how long a connection that stopped waits before it restarts.
var RelaunchWait = 5 * time.Second

// Options holds the command line defaults a payload does not set.
type Options struct {
	DisconnectTimeout int
	P2PTimeout        int
}

// Run runs the profile until it stops or ctx ends. issued is the client key
// of the previous run, and the one to pass to the next is returned.
func (p *Profile) Run(ctx context.Context, issued string, opts Options) (string, error) {
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
func (p *Profile) runHosting(ctx context.Context) error {
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

// Supervise keeps each connection of the plan running, independently
// of the others: when one stops the plan is loaded again after
// RelaunchWait and whatever is not running is started. The first load
// must be valid; a later invalid one is logged and tried again. It returns
// nil when ctx ends and an error when npc cannot continue.
func Supervise(
	ctx context.Context,
	load func(context.Context) (Plan, error),
	run func(context.Context, Profile) error,
) error {
	ctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	type end struct {
		profile Profile
		err     error
	}
	ended := make(chan end)
	running := make(map[string]bool)
	started := false
	var wait <-chan time.Time
	reload := func() error {
		plan, err := load(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && !started:
			return err
		case err != nil:
			log.Warn("launch reload failed", "retry_in_ms", SourceRetry.Milliseconds(), "err", err)
			wait = time.After(SourceRetry)
			return nil
		}
		started = true
		for _, p := range plan.Profiles {
			key := p.Key()
			if running[key] {
				continue
			}
			running[key] = true
			wg.Go(func() {
				err := run(ctx, p)
				select {
				case ended <- end{p, err}:
				case <-ctx.Done():
				}
			})
		}
		if plan.Retry > 0 {
			wait = time.After(plan.Retry)
		}
		return nil
	}
	if err := reload(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-ended:
			delete(running, e.profile.Key())
			err := e.err
			if err == nil {
				err = errors.New("client closed")
			}
			if !launchReconnects(e.profile) {
				return err
			}
			log.Info("launch connection restarting", "name", e.profile.Name, "retry_in_ms", RelaunchWait.Milliseconds(), "err", err)
			wait = time.After(RelaunchWait)
		case <-wait:
			wait = nil
			if err := reload(); err != nil {
				return err
			}
		}
	}
}

func launchReconnects(p Profile) bool {
	return p.Config.CommonConfig.AutoReconnection && client.AutoReconnect
}
