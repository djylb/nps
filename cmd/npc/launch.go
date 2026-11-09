//go:build !sdk

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
	flag "github.com/spf13/pflag"
)

var launchRelaunchWait = 5 * time.Second

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

// legacyAccessFlags are the single-access flags of earlier versions.
var legacyAccessFlags = []string{"password", "local_type", "local_port", "target", "local_proxy", "conn_mode"}

// checkLaunchFlags refuses two ways of saying the same thing: -server or
// -vkey with -launch, and the single-access flags with -launch or -L.
func checkLaunchFlags(fs *flag.FlagSet, hasLaunch bool) error {
	if hasLaunch && (fs.Changed("server") || fs.Changed("vkey")) {
		return errors.New("-launch cannot be combined with -server or -vkey: give the connection in one of them")
	}
	if hasLaunch || fs.Changed("local") {
		for _, name := range legacyAccessFlags {
			if fs.Changed(name) {
				return fmt.Errorf("-%s cannot be combined with -launch or -L: write the access as -L", name)
			}
		}
	}
	return nil
}

// launchSpec turns the command line into the launch model: the -launch
// inputs or the flags of earlier versions, and the -L accesses.
func launchSpec(fs *flag.FlagSet, inputs []string) ([]launch.Source, []launch.Local, error) {
	if err := checkLaunchFlags(fs, len(inputs) > 0); err != nil {
		return nil, nil, err
	}
	values, _ := fs.GetStringArray("local")
	locals, err := launch.ParseLocals(values)
	if err != nil {
		return nil, nil, err
	}
	if len(inputs) > 0 {
		for _, name := range []string{"type", "config", "proxy", "local_ip"} {
			if f := fs.Lookup(name); f != nil && f.Changed {
				cfgLog.Warn("flag ignored", "flag", name, "reason", "launch")
			}
		}
		return launch.LaunchSources(inputs), locals, nil
	}
	sources, err := commandLineSources(fs)
	return sources, locals, err
}

// commandLineSources collects the connection flags of earlier versions and
// their environment variables for launch.CommandLine.
func commandLineSources(fs *flag.FlagSet) ([]launch.Source, error) {
	env := runenv.GetEnvMap()
	or := func(v, key string) string {
		if v == "" {
			return env[key]
		}
		return v
	}
	get := func(name string) string { v, _ := fs.GetString(name); return v }
	port, _ := fs.GetInt("local_port")
	proxy, _ := fs.GetBool("local_proxy")
	cl := launch.CommandLine{
		Server: or(get("server"), "NPC_SERVER_ADDR"), VKey: or(get("vkey"), "NPC_SERVER_VKEY"), Type: get("type"),
		Proxy: get("proxy"), LocalIP: or(get("local_ip"), "NPC_LOCAL_IP"),
		Password: get("password"), LocalType: get("local_type"), ConnMode: get("conn_mode"), Target: get("target"),
		LocalPort: port, LocalProxy: proxy,
	}
	if cl.Password == "" {
		hasCommand, err := commandLineMode(cl.Server, cl.VKey)
		if err != nil {
			return nil, err
		}
		if fs.Changed("type") && hasCommand && !connprofile.UsesType(cl.Server) {
			cfgLog.Warn("-type has no effect", "detail", "every server candidate is a URL, whose scheme names the carrier")
		}
		paths := or(get("config"), "NPC_CONFIG_PATH")
		if paths == "" && !hasCommand {
			paths = runenv.GetConfigPath()
		}
		for p := range strings.SplitSeq(paths, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cl.Config = append(cl.Config, p)
			}
		}
	}
	return cl.Sources()
}

func startLaunch(ctx context.Context, cancel context.CancelFunc, sources []launch.Source, locals []launch.Local) {
	slogx.Emit(context.Background(), log, &logevent.Started, "version", version.VERSION, "role", "npc", "core_version", legacy.CoreVersion(legacy.LatestIndex))
	clock.Sync()
	opts := launch.Options{DisconnectTimeout: *disconnectTime, P2PTimeout: *p2pTime}
	var mu sync.Mutex
	issued := make(map[string]string)
	run := func(ctx context.Context, p launch.Profile) error {
		key := p.Key()
		mu.Lock()
		prev := issued[key]
		mu.Unlock()
		next, err := p.Run(ctx, prev, opts)
		mu.Lock()
		issued[key] = next
		mu.Unlock()
		return err
	}
	composer := launch.NewComposer(sources, locals)
	go func() {
		if err := superviseLaunch(ctx, composer.Load, run); err != nil {
			fail(cancel, err)
		}
	}()
}

// superviseLaunch keeps each connection of the plan running, independently
// of the others: when one stops the plan is loaded again after
// launchRelaunchWait and whatever is not running is started. The first load
// must be valid; a later invalid one is logged and tried again. It returns
// nil when ctx ends and an error when npc cannot continue.
func superviseLaunch(
	ctx context.Context,
	load func(context.Context) (launch.Plan, error),
	run func(context.Context, launch.Profile) error,
) error {
	ctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	type end struct {
		profile launch.Profile
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
			log.Warn("launch reload failed", "retry_in_ms", launch.SourceRetry.Milliseconds(), "err", err)
			wait = time.After(launch.SourceRetry)
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
			log.Info("launch connection restarting", "name", e.profile.Name, "retry_in_ms", launchRelaunchWait.Milliseconds(), "err", err)
			wait = time.After(launchRelaunchWait)
		case <-wait:
			wait = nil
			if err := reload(); err != nil {
				return err
			}
		}
	}
}

func launchReconnects(p launch.Profile) bool {
	return p.Config.CommonConfig.AutoReconnection && client.AutoReconnect
}

const launchUsage = `npc launch pack <file>
    Print the file as a value for -launch (gzip and base64).
    The value contains every key of the file in clear text: treat it as a secret.
npc launch show [-launch=... ...] [-L=... ...] [--reveal]
    Print what the arguments amount to in the current form. Also takes the
    flags of earlier versions (-server, -vkey, -password, ...) and prints
    their current form. vkeys, access keys and proxy passwords are masked
    unless --reveal is given.
`

// launchCommand runs npc launch <args> and returns the exit code.
func launchCommand(fs *flag.FlagSet, args []string) int {
	ctx := context.Background()
	var (
		out string
		err error
	)
	switch {
	case len(args) == 2 && args[0] == "pack":
		out, err = launch.Pack(ctx, args[1])
	case len(args) == 1 && args[0] == "show":
		var sources []launch.Source
		var locals []launch.Local
		if sources, locals, err = launchSpec(fs, launchPayloads()); err == nil {
			out, err = launch.Show(ctx, sources, locals, *reveal)
		}
	default:
		fmt.Fprint(os.Stderr, launchUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "npc launch:", err)
		return 1
	}
	fmt.Println(out)
	return 0
}
