//go:build !sdk

package main

import (
	"context"
	"encoding/hex"
	"os"
	"runtime"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/version"
)

// runCoreProfile runs one server profile on the new protocol.
func runCoreProfile(ctx context.Context, server, vkey, carrier string) {
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
	}), *autoReconnect)
}
