//go:build !sdk

package main

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/version"
)

// runCoreProfile runs one server profile on the new protocol.
func runCoreProfile(ctx context.Context, server, vkey, carrier, localIP string) {
	cands, err := connprofile.Parse(server, carrier)
	if err != nil {
		log.Error("invalid server", "err", err)
		return
	}
	pin, err := hex.DecodeString(*newProtocolPin)
	if err != nil {
		log.Error("invalid pin", "err", err)
		return
	}
	label, _ := os.Hostname()
	client.RunCore(ctx, session.NewDialer(session.DialConfig{
		Candidates: cands,
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
		Exit:           client.NewCoreExit(localIP),
		CompressLimits: session.CompressLimits{MaxWindow: *compressMaxWindow, MaxRatio: *compressMaxRatio},
	}), *autoReconnect)
}
