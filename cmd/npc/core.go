//go:build !sdk

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/config"
	flag "github.com/spf13/pflag"
)

// setCoreDefaults hands the command line to the configuration and the client
// as what a file or payload that does not set a key uses (config.CommonDefaults,
// connect.Process).
func setCoreDefaults() {
	connect.Install()
	config.CommonDefaults = config.Defaults{
		Pipes:       *pipes,
		SchedulerUp: *schedulerUp,
		CoreSettings: config.CoreSettings{
			MaxLanes:      *sessionMaxLanes,
			RelayIdle:     *relayIdle,
			KeepServer:    *p2pVisitorKeepServer,
			ResumeTimeout: *sessionResumeTimeout,
			UsageInterval: *sessionUsageInterval,
			Pin:           *newProtocolPin,
		},
	}
	connect.Process.UDP5Idle = time.Duration(*udp5IdleTimeout) * time.Second
	connect.Process.Compress = session.CompressLimits{MaxWindow: *compressMaxWindow, MaxRatio: *compressMaxRatio}
}

// commandConfig is the profile npc status and npc register connect with.
func commandConfig() *config.CommonConfig {
	cfg := config.NewCommonConfig()
	cfg.Server, cfg.VKey, cfg.Tp, cfg.LocalIP = *serverAddr, *verifyKey, strings.ToLower(*connType), *localIP
	cfg.ProxyUrl = *proxyUrl
	return cfg
}

// commandConn is one connection npc status or npc register acts on.
type commandConn struct {
	name string
	cfg  *config.CommonConfig
}

// commandConns are the connections of npc status and npc register: those of
// -launch (or NPC_LAUNCH), whose local accesses are ignored, or else the one
// of -server, -vkey and -type.
func commandConns(fs *flag.FlagSet, inputs []string) ([]commandConn, error) {
	if len(inputs) == 0 {
		return []commandConn{{cfg: commandConfig()}}, nil
	}
	sources, _, err := launchSpec(fs, inputs)
	if err != nil {
		return nil, err
	}
	plan, err := launch.NewComposer(sources, nil).Load(context.Background())
	if err != nil {
		return nil, err
	}
	var conns []commandConn
	for _, p := range plan.Profiles {
		name := p.Name
		if name == "" {
			name = p.Source
		}
		conns = append(conns, commandConn{name: name, cfg: p.Config.CommonConfig})
	}
	if len(conns) == 0 {
		return nil, errors.New("-launch gives no connection")
	}
	return conns, nil
}

// runCommand applies f to each connection in turn and fails naming those
// it failed on.
func runCommand(conns []commandConn, f func(*config.CommonConfig) error) error {
	var failed []string
	for _, c := range conns {
		if err := f(c.cfg); err != nil {
			log.Error("command failed", "connection", c.name, "err", err)
			failed = append(failed, c.name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// coreProfileEnd is why npc must stop when a profile returned, nil when it
// was stopped or goes on: err is a profile that cannot be made.
func coreProfileEnd(ctx context.Context, err error, autoReconnect bool) error {
	switch {
	case ctx.Err() != nil:
		return nil
	case err != nil:
		return fmt.Errorf("invalid server: %w", err)
	case !autoReconnect:
		return errors.New("the connection to the server ended and auto reconnection is off")
	}
	return nil
}
