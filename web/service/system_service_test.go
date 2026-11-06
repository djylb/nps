package service

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
)

func TestDefaultSystemServiceBridgeDisplayHandlesEmptyQuicALPN(t *testing.T) {
	oldALPN := append([]string(nil), connection.QuicAlpn...)
	connection.QuicAlpn = nil
	defer func() {
		connection.QuicAlpn = oldALPN
	}()

	display := DefaultSystemService{}.BridgeDisplay(&servercfg.Snapshot{
		Bridge: servercfg.BridgeConfig{
			Display: servercfg.BridgeDisplayConfig{
				QUIC: servercfg.BridgeDisplayEndpoint{
					Show: true,
					IP:   "quic.example",
					Port: "7443",
				},
			},
		},
	}, "example.com")

	if !display.QUIC.Enabled {
		t.Fatal("QUIC display should be enabled")
	}
	if display.QUIC.ALPN != "nps" {
		t.Fatalf("QUIC ALPN = %q, want nps fallback", display.QUIC.ALPN)
	}
	if display.QUIC.Addr != "quic.example:7443" {
		t.Fatalf("QUIC Addr = %q, want quic.example:7443", display.QUIC.Addr)
	}
}
