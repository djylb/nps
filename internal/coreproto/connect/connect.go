// Package connect makes the outer session of an npc profile on the core
// protocol from the settings of npc.conf, a launch payload or the command
// line, which all arrive as a config.CommonConfig.
package connect

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
)

// Process holds the settings that are the same for every profile of the
// process and have no npc.conf key. The command line sets them at start-up.
var Process struct {
	Pin      string // hex key id the server must have
	UDP5Idle time.Duration
	Compress session.CompressLimits
}

// Install makes client use this package for the new protocol.
func Install() { client.CoreOuter = Outer }

// Outer is the outer session of cfg's profile to server, not yet dialed.
func Outer(cfg *config.CommonConfig, server string) (coreapi.Outer, error) {
	dc, err := DialConfig(cfg, server)
	if err != nil {
		return nil, err
	}
	return session.NewOuterSessions(session.NewDialer(dc)), nil
}

// DialConfig is the one place the settings of a profile become the dial
// parameters of the new protocol.
func DialConfig(cfg *config.CommonConfig, server string) (session.DialConfig, error) {
	var dc session.DialConfig
	cands, err := connprofile.Parse(server, cfg.Tp)
	if err != nil {
		return dc, err
	}
	connprofile.UseCoreDefaults(cands)
	mode, _ := connprofile.ParsePipes(cfg.Pipes)
	scheduler, _ := connprofile.ParseScheduler(cfg.SchedulerUp)
	pin, err := hex.DecodeString(Process.Pin)
	if err != nil {
		return dc, err
	}
	label, _ := os.Hostname()
	localIP, proxyURL := cfg.LocalIP, cfg.ProxyUrl
	exit := client.NewCoreExit(localIP, Process.UDP5Idle)
	return session.DialConfig{
		Candidates: cands,
		Pipes:      mode,
		Scheduler:  scheduler,
		VKey:       cfg.VKey,
		Pin:        pin,
		Agent:      "npc/" + version.VERSION + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Label:      label,
		Dial: func(ctx context.Context, _, addr string) (net.Conn, error) {
			timeout := 15 * time.Second
			if d, ok := ctx.Deadline(); ok {
				timeout = time.Until(d)
			}
			return client.GetProxyConn(proxyURL, addr, timeout, localIP)
		},
		DialQUIC: func(ctx context.Context, addr string, t *tls.Config, q *quic.Config) (*quic.Conn, error) {
			return conn.DialQuicWithLocalIP(ctx, addr, t, q, localIP)
		},
		MaxLanes:       cfg.MaxLanes,
		Exit:           exit,
		PeerExit:       client.NewCorePeerExit(exit),
		Puncher:        client.NewCorePuncher(localIP, session.QUICConfig),
		Health:         client.NewCoreHealth,
		CompressLimits: Process.Compress,
		ResumeTimeout:  time.Duration(cfg.ResumeTimeout) * time.Second,
	}, nil
}
