package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/stats"
	"github.com/djylb/slogx"
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
		for _, w := range servercfg.Current().Legacy {
			log.Warn("legacy config setting", "detail", w)
		}
		slogx.Emit(context.Background(), log, &logevent.ConfigApplied, "file", r.Path, "keys", strings.Join(r.Applied, ","), "restart", strings.Join(r.Restart, ","))
		if len(r.Restart) > 0 {
			log.Warn("config changes need a restart", "keys", strings.Join(r.Restart, ","))
		}
	} else {
		var keys, errs []string
		for _, f := range r.Failed {
			keys = append(keys, f.Settings...)
			errs = append(errs, f.Name+": "+f.Error)
		}
		reason, err := "apply_failed", strings.Join(errs, "; ")
		if r.Error != "" {
			reason, err = "read_failed", r.Error
		}
		slogx.Emit(context.Background(), log, &logevent.ConfigFailed, "file", r.Path, "keys", strings.Join(keys, ","), "reason", reason, "err", err)
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
		{Name: "log secrets", Settings: []string{"log_secrets"}, Apply: func(_, cur *servercfg.Snapshot) error {
			slogx.Default.SetLogSecrets(cur.Log.Secrets)
			return nil
		}},
		{Name: "log level", Settings: []string{"log_level"}, Apply: func(_, cur *servercfg.Snapshot) error {
			return slogx.Default.Levels().Set(cur.Log.Level)
		}},
		{Name: "log output", Settings: []string{"log", "log_format", "log_path", "security_log", "log_max_files", "log_max_days", "log_max_size", "log_compress", "log_memory", "log_instance", "access_log", "access_log_format"}, Apply: restart},
		{Name: "ntp", Settings: []string{"ntp_server", "ntp_interval"}, Apply: func(_, cur *servercfg.Snapshot) error {
			ApplyNTP(cur)
			return nil
		}},
		{Name: "timezone", Settings: []string{"timezone"}, Apply: restart},
		{Name: "dns", Settings: []string{"dns_server"}, Apply: restart},
		{Name: "pprof", Settings: []string{"pprof_ip", "pprof_port"}, Apply: restart},
		{Name: "access lists", Settings: []string{"geoip_path", "geosite_path", "acl_max_rules"}, Apply: restart},
		{Name: "limits", Settings: []string{"flow_limit_type", "client_history_limit"}, Apply: func(_, cur *servercfg.Snapshot) error {
			ApplyLimits(cur)
			return nil
		}},
		{Name: "system info", Settings: []string{"system_info_display"}, Apply: func(_, _ *servercfg.Snapshot) error {
			stats.StartSystemInfo()
			return nil
		}},
		{Name: "webhook", Settings: []string{
			"webhook_url", "webhook_secret", "webhook_timeout", "webhook_retries",
			"webhook_queue_size", "webhook_report_interval",
		}, Apply: func(_, cur *servercfg.Snapshot) error {
			s.ApplyWebhook(cur.Webhook)
			return nil
		}},
		// read by the tunnels and the bridge where they are used; tunnels
		// started before keep the local proxy setting they started with
		{Name: "tunnels", Settings: []string{
			"http_add_origin_header", "auth_time_window",
		}},
		// read where used as above, and the grants of point-to-point access
		// hold the policy resolved under these: visitors attach again
		{Name: "tunnels and secret grants", Settings: []string{
			"allow_local_proxy", "allow_secret_link", "allow_secret_local",
		}, Apply: func(_, _ *servercfg.Snapshot) error {
			s.bridge.RevokeSecretGrants()
			return nil
		}},
		{Name: "bridge listeners", Settings: []string{
			"bridge_ip", "bridge_host", "bridge_path", "bridge_port",
			"bridge_trusted_ips", "bridge_real_ip_header",
			"bridge_tcp_ip", "bridge_tcp_port", "bridge_kcp_ip", "bridge_kcp_port",
			"bridge_quic_ip", "bridge_quic_port", "bridge_tls_ip", "bridge_tls_port", "tls_bridge_port",
			"bridge_ws_ip", "bridge_ws_port", "bridge_wss_ip", "bridge_wss_port",
			"bridge_type", "tcp_enable", "kcp_enable", "quic_enable", "tls_enable", "ws_enable", "wss_enable",
		}, Apply: restart},
		{Name: "quic transport", Settings: []string{
			"quic_alpn", "quic_keep_alive_period", "quic_max_idle_timeout", "quic_max_incoming_streams",
		}, Apply: restart},
		{Name: "bridge", Settings: []string{
			"bridge_select_mode", "bridge_cert_file", "bridge_key_file", "secure_mode",
			"allow_ports", "public_vkey", "visitor_vkey", "disconnect_timeout", "mux_ping_interval",
		}, Apply: restart},
		{Name: "host proxy", Settings: []string{
			"http_proxy_ip", "http_proxy_port", "https_proxy_port", "http3_proxy_port",
			"http_proxy_response_timeout", "error_page", "error_page_time_limit", "error_page_flow_limit",
			"error_always", "bridge_http3", "force_auto_ssl", "x_nps_http_only",
			"ssl_email", "ssl_ca", "ssl_path", "ssl_zerossl_api", "https_default_cert_file", "https_default_key_file",
			"ssl_cache_max", "ssl_cache_reload", "ssl_cache_idle",
		}, Apply: restart},
		{Name: "p2p", Settings: []string{
			"p2p_ip", "p2p_port", "p2p_stun_servers",
			"p2p_enable_upnp_portmap", "p2p_enable_pcp_portmap", "p2p_enable_natpmp_portmap",
			"p2p_portmap_lease_seconds", "p2p_probe_timeout_ms", "p2p_handshake_timeout_ms", "p2p_transport_timeout_ms",
		}, Apply: restart},
		{Name: "web listener", Settings: []string{
			"web_ip", "web_port", "web_host", "web_open_ssl", "web_cert_file", "web_key_file",
		}, Apply: restart},
		{Name: "ip register listener", Settings: []string{
			"ip_register_ip", "ip_register_port", "ip_register_cert_file", "ip_register_key_file",
		}, Apply: restart},
		{Name: "flow store", Settings: []string{"flow_store_interval"}, Apply: restart},
	}
}
