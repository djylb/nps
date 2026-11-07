//go:build !sdk

package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/timeshim"
	flag "github.com/spf13/pflag"
)

var (
	launchSourceRetry  = 30 * time.Second
	launchSourcePause  = 5 * time.Minute
	launchRelaunchWait = 5 * time.Second
)

func launchPayloads() []string {
	var inputs []string
	for _, input := range *launchInputs {
		if input = strings.TrimSpace(input); input != "" {
			inputs = append(inputs, input)
		}
	}
	if len(inputs) == 0 {
		if env := strings.TrimSpace(os.Getenv("NPC_LAUNCH")); env != "" {
			inputs = append(inputs, env)
		}
	}
	return inputs
}

func startLaunch(ctx context.Context, cancel context.CancelFunc, inputs []string) {
	for _, name := range []string{"server", "vkey", "type", "config", "proxy", "local_ip", "password", "target"} {
		if f := flag.CommandLine.Lookup(name); f != nil && f.Changed {
			cfgLog.Warn("flag ignored because --launch is set", "flag", name)
		}
	}
	obs.Emit(log, &logevent.Started, "version", version.VERSION, "role", "npc", "core_version", legacy.CoreVersion(legacy.LatestIndex))
	clock.Sync()
	opts := launch.Options{DisconnectTimeout: *disconnectTime, P2PTimeout: *p2pTime}
	for i, input := range inputs {
		uuids := make(map[string]string)
		run := func(ctx context.Context, profiles []launch.Profile) error {
			return launch.Run(ctx, profiles, uuids, opts)
		}
		go func() {
			if err := superviseLaunch(ctx, i+1, input, launch.Resolve, run); err != nil {
				log.Error("launch input stopped", "input", i+1, "err", err)
				cancel()
				os.Exit(1)
			}
		}()
	}
}

// superviseLaunch keeps one input running: it resolves the payload, runs its
// profiles and starts over when they stop. A source that fails falls back to
// the last good profiles and is asked again after launchSourceRetry. It
// returns nil when ctx ends and an error when the input cannot continue.
func superviseLaunch(
	ctx context.Context,
	index int,
	input string,
	resolve func(context.Context, string) ([]launch.Profile, error),
	run func(context.Context, []launch.Profile) error,
) error {
	var good []launch.Profile
	var nextResolve time.Time
	for ctx.Err() == nil {
		if good == nil || !time.Now().Before(nextResolve) {
			profiles, err := resolve(ctx, input)
			var remote *launch.RemoteError
			switch {
			case err == nil:
				good = profiles
			case ctx.Err() != nil:
				return nil
			case !errors.As(err, &remote):
				return err
			case remote.Revoked:
				log.Warn("launch input revoked, stopping it", "input", index, "retry_in_ms", launchSourcePause.Milliseconds())
				good = nil
				_ = timeshim.Sleep(ctx, timeshim.Real, launchSourcePause)
				continue
			case good == nil:
				log.Warn("launch input failed", "input", index, "retry_in_ms", launchSourceRetry.Milliseconds(), "err", err)
				_ = timeshim.Sleep(ctx, timeshim.Real, launchSourceRetry)
				continue
			default:
				log.Warn("launch input failed, using the last good payload", "input", index, "err", err)
				nextResolve = time.Now().Add(launchSourceRetry)
			}
		}
		err := run(ctx, good)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("client closed")
		}
		if !launchReconnects(good) {
			return err
		}
		log.Info("launch input closed, restarting", "input", index, "retry_in_ms", launchRelaunchWait.Milliseconds(), "err", err)
		_ = timeshim.Sleep(ctx, timeshim.Real, launchRelaunchWait)
	}
	return nil
}

func launchReconnects(profiles []launch.Profile) bool {
	for _, p := range profiles {
		if !p.Config.CommonConfig.AutoReconnection {
			return false
		}
	}
	return client.AutoReconnect
}
