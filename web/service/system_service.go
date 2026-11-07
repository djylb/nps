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
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
)

type SystemInfo struct {
	Version string
	Year    int
	// CanUpdate tells whether the server can be updated from the web to a
	// newer release.
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
	// UpdateGeoData downloads the latest release of the geodata file called
	// name, geoip or geosite, or of geoip and the files present when name is
	// empty, and applies it.
	UpdateGeoData(name string) (GeoDataStatus, error)
}

// GeoDataStatus describes the geodata files of the access lists.
type GeoDataStatus struct {
	Files []GeoFileStatus
}

// GeoFileStatus describes one geodata file.
type GeoFileStatus struct {
	Name string
	Path string
	// Version is the release the file comes from, "" when unknown.
	Version string
	Exists  bool
	Size    int64
	ModTime string
}

// DefaultSystemService serves the system information of Runtime, the running
// server; a nil Runtime runs nothing and cannot be updated.
type DefaultSystemService struct {
	Runtime Runtime
}

func (s DefaultSystemService) canUpdate() bool {
	return s.Runtime != nil && s.Runtime.CanUpdateSelf()
}

func (s DefaultSystemService) Info() SystemInfo {
	return SystemInfo{
		Version: server.GetVersion(),
		Year:    server.GetCurrentYear(),
		// the update button only appears when a newer release exists
		CanUpdate: s.canUpdate() && install.UpdateAvailable(),
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
	display.TCP = newBridgeTransport(cfg.Bridge.Display.TCP.Show, "tcp", primary.IP, cfg.Bridge.Display.TCP.IP, strconv.Itoa(connection.Network().BridgeTCPPort), cfg.Bridge.Display.TCP.Port, "", false)
	display.KCP = newBridgeTransport(cfg.Bridge.Display.KCP.Show, "kcp", primary.IP, cfg.Bridge.Display.KCP.IP, strconv.Itoa(connection.Network().BridgeKCPPort), cfg.Bridge.Display.KCP.Port, "", false)
	display.TLS = newBridgeTransport(cfg.Bridge.Display.TLS.Show, "tls", primary.IP, cfg.Bridge.Display.TLS.IP, strconv.Itoa(connection.Network().BridgeTLSPort), cfg.Bridge.Display.TLS.Port, "", false)
	display.QUIC = newBridgeTransport(cfg.Bridge.Display.QUIC.Show, "quic", primary.IP, cfg.Bridge.Display.QUIC.IP, strconv.Itoa(connection.Network().BridgeQUICPort), cfg.Bridge.Display.QUIC.Port, "", false)
	display.QUIC.ALPN = defaultString(cfg.Bridge.Display.QUIC.ALPN, defaultQuicALPN())
	if display.QUIC.Enabled && display.QUIC.ALPN != "" && display.QUIC.ALPN != "nps" {
		display.QUIC.Addr += "/" + display.QUIC.ALPN
	}
	display.WS = newBridgeTransport(cfg.Bridge.Display.WS.Show, "ws", primary.IP, cfg.Bridge.Display.WS.IP, strconv.Itoa(connection.Network().BridgeWSPort), cfg.Bridge.Display.WS.Port, display.Path, true)
	display.WSS = newBridgeTransport(cfg.Bridge.Display.WSS.Show, "wss", primary.IP, cfg.Bridge.Display.WSS.IP, strconv.Itoa(connection.Network().BridgeWSSPort), cfg.Bridge.Display.WSS.Port, display.Path, true)
	return display
}

func (s DefaultSystemService) UpdateServer() (string, error) {
	if !s.canUpdate() {
		return "", ErrServerUpdateUnavailable
	}
	v, err := s.Runtime.UpdateSelf()
	if errors.Is(err, server.ErrUpdateRunning) {
		err = ErrServerUpdateRunning
	}
	return v, err
}

func (DefaultSystemService) GeoDataStatus() GeoDataStatus {
	var st GeoDataStatus
	for _, f := range geoFiles() {
		fs := GeoFileStatus{Name: f.Name, Path: f.Path, Version: install.GeoDataVersion(f.Path)}
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

// geoFiles returns the geodata files of the access lists.
func geoFiles() []install.GeoFile {
	return install.GeoFiles(acl.GeoFiles())
}

func (s DefaultSystemService) UpdateGeoData(name string) (GeoDataStatus, error) {
	if !geoUpdating.CompareAndSwap(false, true) {
		return GeoDataStatus{}, ErrGeoDataUpdateRunning
	}
	defer geoUpdating.Store(false)
	files, err := install.PickGeoFiles(geoFiles(), name)
	if err != nil {
		return s.GeoDataStatus(), err
	}
	res, err := install.UpdateGeoData(files)
	if res != nil && len(res.Updated) > 0 {
		file.RefreshAccessLists()
	}
	return s.GeoDataStatus(), err
}

// managementRegisterTTL is how long a web login registers its address.
const managementRegisterTTL = 2 * time.Hour

func (DefaultSystemService) RegisterManagementAccess(remoteAddr string, clientIDs []int, isAdmin bool) {
	ip, ok := netaddr.ParseIP(strings.TrimSpace(remoteAddr))
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
