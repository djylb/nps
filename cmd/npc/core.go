//go:build !sdk

package main

import (
	"context"
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
			NewProtocol:   *newProtocol,
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

// runCoreProfile hosts one server profile of the command line on the new
// protocol.
func runCoreProfile(ctx context.Context, server, vkey, carrier, localIP string) {
	cfg := config.NewCommonConfig()
	cfg.Server, cfg.VKey, cfg.Tp, cfg.LocalIP = server, vkey, strings.ToLower(carrier), localIP
	cfg.ProxyUrl = *proxyUrl
	if err := client.RunCoreProfile(ctx, cfg, *autoReconnect); err != nil {
		log.Error("invalid server", "err", err)
	}
}
