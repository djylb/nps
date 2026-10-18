package service

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
)

type SystemInfo struct {
	Version string
	Year    int
	// CanUpdate tells whether the server can be updated from the web.
	CanUpdate bool
}

type BridgeTransport struct {
	Enabled bool
	Type    string
	IP      string
	Port    string
	Addr    string
	ALPN    string
}

type BridgeDisplay struct {
	Primary BridgeEndpoint
	Path    string
	TCP     BridgeTransport
	KCP     BridgeTransport
	TLS     BridgeTransport
	QUIC    BridgeTransport
	WS      BridgeTransport
	WSS     BridgeTransport
}

type SystemService interface {
	Info() SystemInfo
	BridgeDisplay(*servercfg.Snapshot, string) BridgeDisplay
	RegisterManagementAccess(string)
	// UpdateServer updates the server to the latest release and restarts it
	// shortly. It returns the version updated to, or "" when the running
	// version is the latest.
	UpdateServer() (string, error)
}

type DefaultSystemService struct{}

func (DefaultSystemService) Info() SystemInfo {
	return SystemInfo{
		Version:   server.GetVersion(),
		Year:      server.GetCurrentYear(),
		CanUpdate: server.CanUpdateSelf(),
	}
}

func (DefaultSystemService) BridgeDisplay(cfg *servercfg.Snapshot, host string) BridgeDisplay {
	primary := BestBridge(cfg, host)
	if cfg == nil {
		cfg = servercfg.Current()
	}
	display := BridgeDisplay{
		Primary: primary,
	}
	if cfg.Network.BridgePath != "" {
		display.Path = cfg.Bridge.Display.Path
	}
	display.TCP = newBridgeTransport(cfg.Bridge.Display.TCP.Show, "tcp", primary.IP, cfg.Bridge.Display.TCP.IP, strconv.Itoa(connection.BridgeTcpPort), cfg.Bridge.Display.TCP.Port, "", false)
	display.KCP = newBridgeTransport(cfg.Bridge.Display.KCP.Show, "kcp", primary.IP, cfg.Bridge.Display.KCP.IP, strconv.Itoa(connection.BridgeKcpPort), cfg.Bridge.Display.KCP.Port, "", false)
	display.TLS = newBridgeTransport(cfg.Bridge.Display.TLS.Show, "tls", primary.IP, cfg.Bridge.Display.TLS.IP, strconv.Itoa(connection.BridgeTlsPort), cfg.Bridge.Display.TLS.Port, "", false)
	display.QUIC = newBridgeTransport(cfg.Bridge.Display.QUIC.Show, "quic", primary.IP, cfg.Bridge.Display.QUIC.IP, strconv.Itoa(connection.BridgeQuicPort), cfg.Bridge.Display.QUIC.Port, "", false)
	display.QUIC.ALPN = defaultString(cfg.Bridge.Display.QUIC.ALPN, defaultQuicALPN())
	if display.QUIC.Enabled && display.QUIC.ALPN != "" && display.QUIC.ALPN != "nps" {
		display.QUIC.Addr += "/" + display.QUIC.ALPN
	}
	display.WS = newBridgeTransport(cfg.Bridge.Display.WS.Show, "ws", primary.IP, cfg.Bridge.Display.WS.IP, strconv.Itoa(connection.BridgeWsPort), cfg.Bridge.Display.WS.Port, display.Path, true)
	display.WSS = newBridgeTransport(cfg.Bridge.Display.WSS.Show, "wss", primary.IP, cfg.Bridge.Display.WSS.IP, strconv.Itoa(connection.BridgeWssPort), cfg.Bridge.Display.WSS.Port, display.Path, true)
	return display
}

func (DefaultSystemService) UpdateServer() (string, error) {
	if !server.CanUpdateSelf() {
		return "", ErrServerUpdateUnavailable
	}
	v, err := server.UpdateSelf()
	if errors.Is(err, server.ErrUpdateRunning) {
		err = ErrServerUpdateRunning
	}
	return v, err
}

func (DefaultSystemService) RegisterManagementAccess(remoteAddr string) {
	if server.Bridge == nil {
		return
	}
	clientIP := strings.TrimSpace(common.GetIpByAddr(remoteAddr))
	if clientIP == "" {
		return
	}
	server.Bridge.Register.Store(clientIP, time.Now().Add(2*time.Hour))
}

func newBridgeTransport(enabled bool, transportType, fallbackIP, configuredIP, fallbackPort, configuredPort, path string, appendPath bool) BridgeTransport {
	if !enabled {
		return BridgeTransport{}
	}
	transport := BridgeTransport{
		Enabled: true,
		Type:    transportType,
		IP:      defaultString(configuredIP, fallbackIP),
		Port:    defaultString(configuredPort, fallbackPort),
	}
	transport.Addr = transport.IP + ":" + transport.Port
	if appendPath && path != "" {
		transport.Addr += path
	}
	return transport
}
