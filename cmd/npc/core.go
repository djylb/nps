//go:build !sdk

package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
)

// runCoreProfile runs one server profile on the new protocol.
func runCoreProfile(ctx context.Context, server, vkey, carrier, localIP string) {
	d := coreDialer(server, vkey, carrier, localIP)
	if d != nil {
		client.RunCore(ctx, d, *autoReconnect)
	}
}

// startCoreVisitors makes mgr reach its tasks over the new protocol through
// one on-demand session.
func startCoreVisitors(ctx context.Context, mgr *client.SecretManager, server, vkey, carrier, localIP string) bool {
	first, _, _ := strings.Cut(server, ";")
	d := coreDialer(first, vkey, carrier, localIP)
	if d == nil {
		return false
	}
	outer := session.NewOuterSessions(d)
	outer.Keep = *p2pVisitorKeepServer
	if outer.Keep {
		go keepOuter(ctx, outer)
	}
	mgr.UseCoreVisitors(func(password, mode string) coreapi.Visitor {
		return session.NewVisitor(session.VisitorConfig{Outer: outer, Access: []byte(password), Mode: session.ConnModeOf(mode),
			RelayIdle: time.Duration(*relayIdle) * time.Second})
	})
	go func() {
		<-ctx.Done()
		outer.Close()
	}()
	return true
}

// keepOuter holds the session open for p2p_visitor_keep_server.
func keepOuter(ctx context.Context, outer *session.OuterSessions) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if _, err := outer.Get(ctx); err != nil && ctx.Err() == nil {
			log.Debug("server session not made", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// coreDialer is the dialer of one server profile; nil after logging why not.
func coreDialer(server, vkey, carrier, localIP string) *session.Dialer {
	cands, err := connprofile.Parse(server, carrier)
	if err != nil {
		log.Error("invalid server", "err", err)
		return nil
	}
	connprofile.UseCoreDefaults(cands)
	mode, _ := connprofile.ParsePipes(*pipes)
	scheduler, _ := connprofile.ParseScheduler(*schedulerUp)
	pin, err := hex.DecodeString(*newProtocolPin)
	if err != nil {
		log.Error("invalid pin", "err", err)
		return nil
	}
	label, _ := os.Hostname()
	exit := client.NewCoreExit(localIP, time.Duration(*udp5IdleTimeout)*time.Second)
	return session.NewDialer(session.DialConfig{
		Candidates: cands,
		Pipes:      mode,
		Scheduler:  scheduler,
		VKey:       vkey,
		Pin:        pin,
		Agent:      "npc/" + version.VERSION + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Label:      label,
		Dial: func(ctx context.Context, _, addr string) (net.Conn, error) {
			timeout := 15 * time.Second
			if d, ok := ctx.Deadline(); ok {
				timeout = time.Until(d)
			}
			return client.GetProxyConn(*proxyUrl, addr, timeout, localIP)
		},
		DialQUIC: func(ctx context.Context, addr string, t *tls.Config, q *quic.Config) (*quic.Conn, error) {
			return conn.DialQuicWithLocalIP(ctx, addr, t, q, localIP)
		},
		MaxLanes:       *sessionMaxLanes,
		Exit:           exit,
		PeerExit:       client.NewCorePeerExit(exit),
		Puncher:        client.NewCorePuncher(localIP, session.QUICConfig),
		CompressLimits: session.CompressLimits{MaxWindow: *compressMaxWindow, MaxRatio: *compressMaxRatio},
		ResumeTimeout:  time.Duration(*sessionResumeTimeout) * time.Second,
	})
}
