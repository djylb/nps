package service

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/ipreg"
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
	// RegisterManagementAccess registers the address of a web login for the
	// clients the user manages, or every client for an administrator.
	RegisterManagementAccess(remoteAddr string, clientIDs []int, isAdmin bool)
	// UpdateServer updates the server to the latest release and restarts it
	// shortly. It returns the version updated to, or "" when the running
	// version is the latest.
	UpdateServer() (string, error)
	// GeoDataStatus describes the geodata files of the access lists.
	GeoDataStatus() GeoDataStatus
	// UpdateGeoData downloads the latest geodata files and applies them.
	UpdateGeoData() (GeoDataStatus, error)
}

// GeoDataStatus describes the geodata files of the access lists.
type GeoDataStatus struct {
	// Version is the release the files come from, "" when unknown.
	Version string
	Files   []GeoFileStatus
}

// GeoFileStatus describes one geodata file.
type GeoFileStatus struct {
	Name    string
	Path    string
	Exists  bool
	Size    int64
	ModTime string
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

func (DefaultSystemService) GeoDataStatus() GeoDataStatus {
	ipPath, sitePath := acl.GeoFiles()
	st := GeoDataStatus{Version: install.GeoDataVersion(ipPath)}
	for _, f := range install.GeoFiles(ipPath, sitePath) {
		fs := GeoFileStatus{Name: f.Name, Path: f.Path}
		if fi, err := os.Stat(f.Path); err == nil {
			fs.Exists = true
			fs.Size = fi.Size()
			fs.ModTime = fi.ModTime().Format("2006-01-02 15:04:05")
		}
		st.Files = append(st.Files, fs)
	}
	return st
}

// geoUpdating keeps a single geodata update running.
var geoUpdating atomic.Bool

func (s DefaultSystemService) UpdateGeoData() (GeoDataStatus, error) {
	if !geoUpdating.CompareAndSwap(false, true) {
		return GeoDataStatus{}, ErrGeoDataUpdateRunning
	}
	defer geoUpdating.Store(false)
	ipPath, sitePath := acl.GeoFiles()
	res, err := install.UpdateGeoData(ipPath, sitePath)
	if res != nil && len(res.Updated) > 0 {
		file.GetDb().RefreshAccessLists()
	}
	return s.GeoDataStatus(), err
}

// managementRegisterTTL is how long a web login registers its address.
const managementRegisterTTL = 2 * time.Hour

func (DefaultSystemService) RegisterManagementAccess(remoteAddr string, clientIDs []int, isAdmin bool) {
	ip, ok := ipreg.ParseIP(strings.TrimSpace(remoteAddr))
	if !ok {
		return
	}
	if isAdmin {
		ipreg.Register(ipreg.AnyClient, ip, managementRegisterTTL)
		return
	}
	for _, id := range clientIDs {
		if id > 0 {
			ipreg.Register(id, ip, managementRegisterTTL)
		}
	}
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
