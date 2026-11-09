//go:build !sdk

package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
	"github.com/djylb/timeshim"
	flag "github.com/spf13/pflag"
)

var (
	launchSourceRetry  = 30 * time.Second
	launchSourcePause  = 5 * time.Minute
	launchRelaunchWait = 5 * time.Second
)

// launchPayloads are the inputs of -launch, or, when the command line gives
// none, those of NPC_LAUNCH.
func launchPayloads() []string {
	var inputs []string
	for _, input := range *launchInputs {
		if input = strings.TrimSpace(input); input != "" {
			inputs = append(inputs, input)
		}
	}
	if len(inputs) == 0 {
		inputs = splitLaunchEnv(os.Getenv("NPC_LAUNCH"))
	}
	return inputs
}

// splitLaunchEnv splits the value of NPC_LAUNCH into inputs, one per line:
// a launch link has commas of its own (alpn=h3,nps), so only a line break
// separates them. Empty lines are ignored; a line may be @file.
func splitLaunchEnv(env string) []string {
	var inputs []string
	for line := range strings.SplitSeq(strings.ReplaceAll(env, "\r", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			inputs = append(inputs, line)
		}
	}
	return inputs
}

// accessFlags are the point-to-point visitor flags with the launch link
// parameter each one is.
var accessFlags = map[string]string{
	"local_type": "local_type", "local_port": "local_port", "conn_mode": "conn_mode",
	"target": "target", "local_proxy": "local_proxy",
}

// accessValue is the access key of a point-to-point visitor: -access, or its
// alias -password. Giving both is an error.
func accessValue(fs *flag.FlagSet) (string, error) {
	access, _ := fs.GetString("access")
	password, _ := fs.GetString("password")
	if fs.Changed("access") && fs.Changed("password") {
		return "", errors.New("-access and -password are the same setting: give one")
	}
	if access == "" {
		access = password
	}
	return access, nil
}

// checkLaunchFlags refuses -server or -vkey together with -launch: they are
// two ways to say which server to connect to.
func checkLaunchFlags(fs *flag.FlagSet) error {
	if fs.Changed("server") || fs.Changed("vkey") {
		return errors.New("-launch cannot be combined with -server or -vkey: give the connection in one of them")
	}
	return nil
}

// withAccess adds the point-to-point visitor flags of fs to each launch
// link in inputs. They only apply to links; a link that already sets a
// parameter the flags set is an error, and so is any other input.
func withAccess(fs *flag.FlagSet, inputs []string) ([]string, error) {
	access, err := accessValue(fs)
	if err != nil || access == "" {
		return inputs, err
	}
	out := make([]string, len(inputs))
	for i, in := range inputs {
		if !connprofile.IsLink(in) {
			return nil, errors.New("-access applies to npc:// launch links only")
		}
		vals := url.Values{"access": {access}}
		l, err := connprofile.ParseLink(connprofile.AppendQuery(in, vals))
		if err != nil {
			return nil, fmt.Errorf("-access with the launch link: %w", err)
		}
		for name, key := range accessFlags {
			if f := fs.Lookup(name); f != nil && (f.Changed || name == "local_port" && l.Local["local_port"] == "") {
				if v := f.Value.String(); v != "" {
					vals.Set(key, v)
				}
			}
		}
		out[i] = connprofile.AppendQuery(in, vals)
		if _, err := connprofile.ParseLink(out[i]); err != nil {
			return nil, fmt.Errorf("-access and the other visitor flags with the launch link: %w", err)
		}
	}
	return out, nil
}

func startLaunch(ctx context.Context, cancel context.CancelFunc, inputs []string) {
	if err := checkLaunchFlags(flag.CommandLine); err != nil {
		fail(cancel, err)
	}
	inputs, err := withAccess(flag.CommandLine, inputs)
	if err != nil {
		fail(cancel, err)
	}
	for _, name := range []string{"type", "config", "proxy", "local_ip"} {
		if f := flag.CommandLine.Lookup(name); f != nil && f.Changed {
			cfgLog.Warn("flag ignored", "flag", name, "reason", "launch")
		}
	}
	slogx.Emit(context.Background(), log, &logevent.Started, "version", version.VERSION, "role", "npc", "core_version", legacy.CoreVersion(legacy.LatestIndex))
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
				log.Warn("launch input revoked", "input", index, "retry_in_ms", launchSourcePause.Milliseconds())
				good = nil
				_ = timeshim.Sleep(ctx, timeshim.Real, launchSourcePause)
				continue
			case good == nil:
				log.Warn("launch input failed", "input", index, "retry_in_ms", launchSourceRetry.Milliseconds(), "err", err)
				_ = timeshim.Sleep(ctx, timeshim.Real, launchSourceRetry)
				continue
			default:
				log.Warn("launch input failed", "input", index, "detail", "using the last good payload", "err", err)
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
		log.Info("launch input restarting", "input", index, "retry_in_ms", launchRelaunchWait.Milliseconds(), "err", err)
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
