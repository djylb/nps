package routers

import (
	"net/http"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

// ConfigApplier returns the applier of the settings of the web manager: when
// one changes, it builds the handler again as Init does and hands it to set.
// The handler is built once the new settings are current, from the same
// arguments as Init.
func ConfigApplier(db *file.DbUtils, identity *crypt.Identity, runtime webservice.Runtime, set func(http.Handler)) servercfg.Applier {
	return servercfg.Applier{
		Name:     "web manager",
		Settings: webSettings,
		Apply: func(_, _ *servercfg.Snapshot) error {
			handler, err := Init(db, identity, runtime)
			if err != nil {
				return err
			}
			set(handler)
			return nil
		},
	}
}

// webSettings are the config keys the web manager reads, at Init or for
// each request; its listener is not among them.
var webSettings = []string{
	"appname", "ip_register_max_hours",
	"instance_key_file", "web_base_url", "head_custom_code", "web_close_on_not_found", "web_tunnel_port_range_max",
	"web_username", "web_password", "totp_secret",
	"auth_key", "auth_crypt_key", "allow_x_real_ip", "trusted_proxy_ips",
	"allow_user_login", "allow_user_register", "allow_user_vkey_login",
	"allow_user_change_username", "open_captcha", "allow_flow_limit",
	"allow_rate_limit", "allow_time_limit", "allow_connection_num_limit",
	"allow_multi_ip", "allow_tunnel_num_limit", "allow_user_local",
	"force_pow", "pow_bits", "login_ban_time", "login_ip_ban_time",
	"login_user_ban_time", "login_max_fail_times", "login_max_body",
	"login_max_skew", "login_acl_mode", "login_acl_rules",
	"http_proxy_access_port", "https_proxy_access_port",
	"bridge_addr", "bridge_show_path",
	"bridge_tcp_show", "bridge_tcp_show_ip", "bridge_tcp_show_port",
	"bridge_kcp_show", "bridge_kcp_show_ip", "bridge_kcp_show_port",
	"bridge_quic_show", "bridge_quic_show_ip", "bridge_quic_show_port", "bridge_quic_show_alpn",
	"bridge_tls_show", "bridge_tls_show_ip", "bridge_tls_show_port",
	"bridge_ws_show", "bridge_ws_show_ip", "bridge_ws_show_port",
	"bridge_wss_show", "bridge_wss_show_ip", "bridge_wss_show_port",
}
