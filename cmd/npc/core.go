//go:build !sdk

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/config"
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
		},
	}
	connect.Process.Pin = *newProtocolPin
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

// runCoreProfile hosts one server profile of the command line on the new
// protocol.
func runCoreProfile(ctx context.Context, cancel context.CancelFunc, server, vkey, carrier, localIP string) {
	cfg := config.NewCommonConfig()
	cfg.Server, cfg.VKey, cfg.Tp, cfg.LocalIP = server, vkey, strings.ToLower(carrier), localIP
	cfg.ProxyUrl = *proxyUrl
	if err := coreProfileEnd(ctx, client.RunCoreProfile(ctx, cfg, *autoReconnect), *autoReconnect); err != nil {
		fail(cancel, err)
	}
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
