package server

import (
	"sync"
	"time"

	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/stats"
)

// configAppliers are the appliers added with AddConfigApplier.
type configAppliers struct {
	sync.Mutex
	extra []servercfg.Applier
}

// AddConfigApplier adds a, applied after those of the server in a reload.
func (s *Server) AddConfigApplier(a servercfg.Applier) {
	s.appliers.Lock()
	defer s.appliers.Unlock()
	s.appliers.extra = append(s.appliers.extra, a)
}

// ConfigAppliers returns the appliers of a reload in their order: those of
// the process and the server, then those added with AddConfigApplier.
func (s *Server) ConfigAppliers() []servercfg.Applier {
	s.appliers.Lock()
	defer s.appliers.Unlock()
	return append(s.builtinAppliers(), s.appliers.extra...)
}

// ReloadConfig reloads the config file and logs and returns the outcome.
func (s *Server) ReloadConfig() servercfg.Report {
	r := servercfg.Reload(s.ConfigAppliers()...)
	if r.OK() {
		if len(r.Restart) > 0 {
			logs.Warn("%s", r)
		} else {
			logs.Info("%s", r)
		}
	} else {
		logs.Error("%s", r)
	}
	return r
}

// ApplyNTP starts the time sync as cfg says.
func ApplyNTP(cfg *servercfg.Snapshot) {
	clock.SetNtpServer(cfg.App.NTPServer)
	clock.SetNtpInterval(time.Duration(cfg.App.NTPInterval) * time.Minute)
	go clock.Sync()
}

// ApplyLimits hands the flow and history settings to lib/model.
func ApplyLimits(cfg *servercfg.Snapshot) {
	model.SetFlowLimitActual(cfg.Feature.FlowLimitActual)
	model.SetClientHistoryLimit(cfg.Runtime.ClientHistoryLimit)
}

func (s *Server) builtinAppliers() []servercfg.Applier {
	restart := servercfg.RestartRequired
	return []servercfg.Applier{
		{Name: "log level", Settings: []string{"Log.Level"}, Apply: func(_, cur *servercfg.Snapshot) error {
			logs.SetLevel(cur.Log.Level)
			return nil
		}},
		{Name: "log output", Settings: []string{"Log.Type", "Log.Path", "Log.MaxFiles", "Log.MaxDays", "Log.MaxSize", "Log.Compress", "Log.Color"}, Apply: restart},
		{Name: "ntp", Settings: []string{"App.NTPServer", "App.NTPInterval"}, Apply: func(_, cur *servercfg.Snapshot) error {
			ApplyNTP(cur)
			return nil
		}},
		{Name: "timezone", Settings: []string{"App.Timezone"}, Apply: restart},
		{Name: "dns", Settings: []string{"App.DNSServer"}, Apply: restart},
		{Name: "pprof", Settings: []string{"App.PprofIP", "App.PprofPort"}, Apply: restart},
		{Name: "access lists", Settings: []string{"App.GeoIPPath", "App.GeoSitePath", "App.AclMaxRules"}, Apply: restart},
		{Name: "limits", Settings: []string{"Feature.FlowLimitActual", "Runtime.ClientHistoryLimit"}, Apply: func(_, cur *servercfg.Snapshot) error {
			ApplyLimits(cur)
			return nil
		}},
		{Name: "system info", Settings: []string{"Feature.SystemInfoDisplay"}, Apply: func(_, _ *servercfg.Snapshot) error {
			stats.StartSystemInfo()
			return nil
		}},
		{Name: "webhook", Settings: []string{"Webhook"}, Apply: func(_, cur *servercfg.Snapshot) error {
			s.ApplyWebhook(cur.Webhook)
			return nil
		}},
		// read by the tunnels and the bridge where they are used; tunnels
		// started before keep the local proxy setting they started with
		{Name: "tunnels", Settings: []string{
			"Feature.AllowLocalProxy", "Feature.AllowSecretLink", "Feature.AllowSecretLocal",
			"Proxy.AddOriginHeader", "Security.AuthTimeWindow",
		}},
		{Name: "bridge listeners", Settings: []string{
			"Network.BridgeIP", "Network.BridgeHost", "Network.BridgePath", "Network.BridgePort",
			"Network.BridgeTrustedIPs", "Network.BridgeRealIPHeader",
			"Network.BridgeTCPIP", "Network.BridgeTCPPort", "Network.BridgeKCPIP", "Network.BridgeKCPPort",
			"Network.BridgeQUICIP", "Network.BridgeQUICPort", "Network.BridgeTLSIP", "Network.BridgeTLSPort",
			"Network.BridgeWSIP", "Network.BridgeWSPort", "Network.BridgeWSSIP", "Network.BridgeWSSPort",
			"Bridge.Type", "Bridge.PrimaryType",
			"Bridge.TCPEnable", "Bridge.KCPEnable", "Bridge.QUICEnable", "Bridge.TLSEnable", "Bridge.WSEnable", "Bridge.WSSEnable",
			"Bridge.ServerTCPEnabled", "Bridge.ServerKCPEnabled", "Bridge.ServerQUICEnabled",
			"Bridge.ServerTLSEnabled", "Bridge.ServerWSEnabled", "Bridge.ServerWSSEnabled",
		}, Apply: restart},
		{Name: "quic transport", Settings: []string{
			"Network.QUICALPN", "Network.QUICALPNList", "Network.QUICKeepAlivePeriod",
			"Network.QUICMaxIdleTimeout", "Network.QUICMaxIncomingStreams",
		}, Apply: restart},
		{Name: "bridge", Settings: []string{
			"Bridge.SelectMode", "Bridge.CertFile", "Bridge.KeyFile", "Security.SecureMode",
			"Feature.AllowPorts", "Runtime.PublicVKey", "Runtime.VisitorVKey",
			"Runtime.DisconnectTimeout", "Network.MuxPingInterval",
		}, Apply: restart},
		{Name: "host proxy", Settings: []string{
			"Network.HTTPProxyIP", "Network.HTTPProxyPort", "Network.HTTPSProxyPort", "Network.HTTP3ProxyPort",
			"Proxy.ResponseTimeout", "Proxy.ErrorPage", "Proxy.ErrorPageTimeLimit", "Proxy.ErrorPageFlowLimit",
			"Proxy.ErrorAlways", "Proxy.BridgeHTTP3", "Proxy.ForceAutoSSL", "Proxy.SSL", "Auth.HTTPOnlyPass",
		}, Apply: restart},
		{Name: "p2p", Settings: []string{"Network.P2PIP", "Network.P2PPort", "P2P"}, Apply: restart},
		{Name: "web listener", Settings: []string{
			"Network.WebIP", "Network.WebPort", "Web.IP", "Web.Port", "Web.Host",
			"Web.OpenSSL", "Web.CertFile", "Web.KeyFile",
		}, Apply: restart},
		{Name: "ip register listener", Settings: []string{
			"App.IPRegisterIP", "App.IPRegisterPort", "App.IPRegisterCertFile", "App.IPRegisterKeyFile",
		}, Apply: restart},
		{Name: "flow store", Settings: []string{"Runtime.FlowStoreInterval"}, Apply: restart},
	}
}
