package servercfg

import (
	"strconv"
	"strings"
	"sync/atomic"
)

type Snapshot struct {
	App      AppConfig
	Log      LogConfig
	Web      WebConfig
	Auth     AuthConfig
	Feature  FeatureConfig
	Security SecurityConfig
	Runtime  RuntimeConfig
	Network  NetworkConfig
	Bridge   BridgeConfig
	Proxy    ProxyConfig
	P2P      P2PConfig
}

type AppConfig struct {
	Name        string
	Timezone    string
	DNSServer   string
	PprofIP     string
	PprofPort   string
	NTPServer   string
	NTPInterval int
}

type LogConfig struct {
	Type     string
	Level    string
	Path     string
	MaxFiles int
	MaxDays  int
	MaxSize  int
	Compress bool
	Color    bool
}

type WebConfig struct {
	BaseURL         string
	HeadCustomCode  string
	Host            string
	IP              string
	Port            int
	OpenSSL         bool
	CloseOnNotFound bool
	KeyFile         string
	CertFile        string
	Username        string
	Password        string
	TOTPSecret      string
}

type AuthConfig struct {
	Key             string
	CryptKey        string
	HTTPOnlyPass    string
	AllowXRealIP    bool
	TrustedProxyIPs string
}

type FeatureConfig struct {
	AllowUserLogin          bool
	AllowUserRegister       bool
	AllowUserVkeyLogin      bool
	AllowUserChangeUsername bool
	OpenCaptcha             bool
	AllowFlowLimit          bool
	AllowRateLimit          bool
	AllowTimeLimit          bool
	AllowConnectionNumLimit bool
	AllowMultiIP            bool
	AllowTunnelNumLimit     bool
	AllowLocalProxy         bool
	AllowUserLocal          bool
	AllowSecretLink         bool
	AllowSecretLocal        bool
	SystemInfoDisplay       bool
	AllowPorts              string
}

type SecurityConfig struct {
	SecureMode        bool
	ForcePoW          bool
	PoWBits           int
	LoginBanTime      int64
	LoginIPBanTime    int64
	LoginUserBanTime  int64
	LoginMaxFailTimes int
	LoginMaxBody      int64
	LoginMaxSkew      int64
}

type RuntimeConfig struct {
	PublicVKey        string
	IPLimit           string
	DisconnectTimeout int
	FlowStoreInterval int
}

type NetworkConfig struct {
	BridgeIP               string
	BridgeHost             string
	BridgeTCPIP            string
	BridgeKCPIP            string
	BridgeQUICIP           string
	BridgeTLSIP            string
	BridgeWSIP             string
	BridgeWSSIP            string
	BridgePort             int
	BridgeTCPPort          int
	BridgeKCPPort          int
	BridgeQUICPort         int
	BridgeTLSPort          int
	BridgeWSPort           int
	BridgeWSSPort          int
	BridgePath             string
	BridgeTrustedIPs       string
	BridgeRealIPHeader     string
	HTTPProxyIP            string
	HTTPProxyPort          int
	HTTPSProxyPort         int
	HTTP3ProxyPort         int
	WebIP                  string
	WebPort                int
	P2PIP                  string
	P2PPort                int
	QUICALPN               string
	QUICALPNList           []string
	QUICKeepAlivePeriod    int
	QUICMaxIdleTimeout     int
	QUICMaxIncomingStreams int64
	MuxPingInterval        int
}

type BridgeConfig struct {
	Addr              string
	Type              string
	PrimaryType       string
	SelectMode        string
	CertFile          string
	KeyFile           string
	TCPEnable         bool
	KCPEnable         bool
	QUICEnable        bool
	TLSEnable         bool
	WSEnable          bool
	WSSEnable         bool
	ServerTCPEnabled  bool
	ServerKCPEnabled  bool
	ServerQUICEnabled bool
	ServerTLSEnabled  bool
	ServerWSEnabled   bool
	ServerWSSEnabled  bool
	Display           BridgeDisplayConfig
}

type BridgeDisplayConfig struct {
	Path string
	TCP  BridgeDisplayEndpoint
	KCP  BridgeDisplayEndpoint
	QUIC BridgeDisplayEndpoint
	TLS  BridgeDisplayEndpoint
	WS   BridgeDisplayEndpoint
	WSS  BridgeDisplayEndpoint
}

type BridgeDisplayEndpoint struct {
	Show bool
	IP   string
	Port string
	ALPN string
}

type ProxyConfig struct {
	AddOriginHeader    bool
	ResponseTimeout    int
	ErrorPage          string
	ErrorPageTimeLimit string
	ErrorPageFlowLimit string
	ErrorAlways        bool
	BridgeHTTP3        bool
	ForceAutoSSL       bool
	SSL                SSLConfig
}

type SSLConfig struct {
	Email           string
	CA              string
	Path            string
	ZeroSSLAPI      string
	DefaultCertFile string
	DefaultKeyFile  string
	CacheMax        int
	CacheReload     int
	CacheIdle       int
}

type P2PConfig struct {
	STUNServers         string
	EnableUPNPPortmap   bool
	EnablePCPPortmap    bool
	EnableNATPMPPortmap bool
	PortmapLeaseSeconds int
	ProbeTimeoutMs      int
	HandshakeTimeoutMs  int
	TransportTimeoutMs  int
}

type valueReader struct {
	values map[string]any
}

var (
	currentSnapshot atomic.Pointer[Snapshot]
	defaultSnapshot = buildSnapshot(nil)
)

func init() {
	currentSnapshot.Store(defaultSnapshot)
}

func Current() *Snapshot {
	if snapshot := currentSnapshot.Load(); snapshot != nil {
		return snapshot
	}
	return defaultSnapshot
}

func buildSnapshot(values map[string]any) *Snapshot {
	r := valueReader{values: values}
	cfg := &Snapshot{}

	cfg.App = AppConfig{
		Name:        r.stringDefault("nps", "appname"),
		Timezone:    r.stringValue("timezone"),
		DNSServer:   r.stringValue("dns_server"),
		PprofIP:     r.stringValue("pprof_ip"),
		PprofPort:   r.stringDefault("0", "pprof_port"),
		NTPServer:   r.stringValue("ntp_server"),
		NTPInterval: r.intDefault(5, "ntp_interval"),
	}

	cfg.Log = LogConfig{
		Type:     r.stringDefault("stdout", "log"),
		Level:    r.stringDefault("trace", "log_level"),
		Path:     r.stringValue("log_path"),
		MaxFiles: r.intDefault(30, "log_max_files"),
		MaxDays:  r.intDefault(30, "log_max_days"),
		MaxSize:  r.intDefault(5, "log_max_size"),
		Compress: r.boolDefault(false, "log_compress"),
		Color:    r.boolDefault(true, "log_color"),
	}

	cfg.Web = WebConfig{
		BaseURL:         r.stringValue("web_base_url"),
		HeadCustomCode:  r.stringValue("head_custom_code"),
		Host:            r.stringValue("web_host"),
		IP:              r.stringDefault("0.0.0.0", "web_ip"),
		Port:            r.intDefault(0, "web_port"),
		OpenSSL:         r.boolDefault(false, "web_open_ssl"),
		CloseOnNotFound: r.boolDefault(false, "web_close_on_not_found"),
		KeyFile:         r.stringValue("web_key_file"),
		CertFile:        r.stringValue("web_cert_file"),
		Username:        r.stringValue("web_username"),
		Password:        r.stringValue("web_password"),
		TOTPSecret:      r.stringValue("totp_secret"),
	}

	cfg.Auth = AuthConfig{
		Key:             r.stringValue("auth_key"),
		CryptKey:        r.stringValue("auth_crypt_key"),
		HTTPOnlyPass:    r.stringValue("x_nps_http_only"),
		AllowXRealIP:    r.boolDefault(false, "allow_x_real_ip"),
		TrustedProxyIPs: r.stringDefault("127.0.0.1", "trusted_proxy_ips"),
	}

	cfg.Feature = FeatureConfig{
		AllowUserLogin:          r.boolDefault(false, "allow_user_login"),
		AllowUserRegister:       r.boolDefault(false, "allow_user_register"),
		AllowUserChangeUsername: r.boolDefault(false, "allow_user_change_username"),
		OpenCaptcha:             r.boolDefault(false, "open_captcha"),
		AllowFlowLimit:          r.boolDefault(false, "allow_flow_limit"),
		AllowRateLimit:          r.boolDefault(false, "allow_rate_limit"),
		AllowTimeLimit:          r.boolDefault(false, "allow_time_limit"),
		AllowConnectionNumLimit: r.boolDefault(false, "allow_connection_num_limit"),
		AllowMultiIP:            r.boolDefault(false, "allow_multi_ip"),
		AllowTunnelNumLimit:     r.boolDefault(false, "allow_tunnel_num_limit"),
		AllowLocalProxy:         r.boolDefault(false, "allow_local_proxy"),
		AllowSecretLink:         r.boolDefault(false, "allow_secret_link"),
		AllowSecretLocal:        r.boolDefault(false, "allow_secret_local"),
		SystemInfoDisplay:       r.boolDefault(false, "system_info_display"),
		AllowPorts:              r.stringValue("allow_ports"),
	}
	cfg.Feature.AllowUserLocal = r.boolDefault(cfg.Feature.AllowLocalProxy, "allow_user_local")
	cfg.Feature.AllowUserVkeyLogin = r.boolDefault(cfg.Feature.AllowUserLogin, "allow_user_vkey_login")

	cfg.Security = SecurityConfig{
		SecureMode:        r.boolDefault(false, "secure_mode"),
		ForcePoW:          r.boolDefault(false, "force_pow"),
		PoWBits:           r.intDefault(20, "pow_bits"),
		LoginBanTime:      r.int64Default(5, "login_ban_time"),
		LoginIPBanTime:    r.int64Default(180, "login_ip_ban_time"),
		LoginUserBanTime:  r.int64Default(3600, "login_user_ban_time"),
		LoginMaxFailTimes: r.intDefault(10, "login_max_fail_times"),
		LoginMaxBody:      r.int64Default(1024, "login_max_body"),
		LoginMaxSkew:      r.int64Default(5*60*1000, "login_max_skew"),
	}

	cfg.Runtime = RuntimeConfig{
		PublicVKey:        r.stringValue("public_vkey"),
		IPLimit:           r.stringValue("ip_limit"),
		DisconnectTimeout: r.intDefault(30, "disconnect_timeout"),
		FlowStoreInterval: r.intDefault(0, "flow_store_interval"),
	}

	cfg.Network.BridgeIP = r.stringDefault(r.stringDefault("0.0.0.0", "bridge_tcp_ip"), "bridge_ip")
	cfg.Network.BridgeHost = r.stringValue("bridge_host")
	cfg.Network.BridgeTCPIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_tcp_ip")
	cfg.Network.BridgeKCPIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_kcp_ip")
	cfg.Network.BridgeQUICIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_quic_ip")
	cfg.Network.BridgeTLSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_tls_ip")
	cfg.Network.BridgeWSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_ws_ip")
	cfg.Network.BridgeWSSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_wss_ip")
	cfg.Network.BridgePort = r.intDefault(r.intDefault(0, "bridge_tcp_port"), "bridge_port")
	cfg.Network.BridgeTCPPort = r.intDefault(cfg.Network.BridgePort, "bridge_tcp_port")
	cfg.Network.BridgeKCPPort = r.intDefault(cfg.Network.BridgePort, "bridge_kcp_port")
	cfg.Network.BridgeQUICPort = r.intDefault(0, "bridge_quic_port")
	cfg.Network.BridgeTLSPort = r.intDefault(r.intDefault(0, "tls_bridge_port"), "bridge_tls_port")
	cfg.Network.BridgeWSPort = r.intDefault(0, "bridge_ws_port")
	cfg.Network.BridgeWSSPort = r.intDefault(0, "bridge_wss_port")
	cfg.Network.BridgePath = r.stringDefault("/ws", "bridge_path")
	cfg.Network.BridgeTrustedIPs = r.stringValue("bridge_trusted_ips")
	cfg.Network.BridgeRealIPHeader = r.stringValue("bridge_real_ip_header")
	cfg.Network.HTTPProxyIP = r.stringDefault("0.0.0.0", "http_proxy_ip")
	cfg.Network.HTTPProxyPort = r.intDefault(0, "http_proxy_port")
	cfg.Network.HTTPSProxyPort = r.intDefault(0, "https_proxy_port")
	cfg.Network.HTTP3ProxyPort = r.intDefault(cfg.Network.HTTPSProxyPort, "http3_proxy_port")
	cfg.Network.WebIP = cfg.Web.IP
	cfg.Network.WebPort = cfg.Web.Port
	cfg.Network.P2PIP = r.stringValue("p2p_ip")
	cfg.Network.P2PPort = r.intDefault(0, "p2p_port")
	cfg.Network.QUICALPN = r.stringDefault("nps", "quic_alpn")
	cfg.Network.QUICALPNList = splitAndTrimCSV(cfg.Network.QUICALPN)
	if len(cfg.Network.QUICALPNList) == 0 {
		cfg.Network.QUICALPNList = []string{"nps"}
	}
	cfg.Network.QUICKeepAlivePeriod = r.intDefault(10, "quic_keep_alive_period")
	cfg.Network.QUICMaxIdleTimeout = r.intDefault(30, "quic_max_idle_timeout")
	cfg.Network.QUICMaxIncomingStreams = r.int64Default(100000, "quic_max_incoming_streams")
	cfg.Network.MuxPingInterval = r.intDefault(5, "mux_ping_interval")

	cfg.Bridge = BridgeConfig{
		Addr:       r.stringValue("bridge_addr"),
		Type:       strings.ToLower(strings.TrimSpace(r.stringDefault("both", "bridge_type"))),
		SelectMode: r.stringValue("bridge_select_mode"),
		CertFile:   r.stringValue("bridge_cert_file"),
		KeyFile:    r.stringValue("bridge_key_file"),
		TCPEnable:  r.boolDefault(true, "tcp_enable"),
		KCPEnable:  r.boolDefault(true, "kcp_enable"),
		QUICEnable: r.boolDefault(true, "quic_enable"),
		TLSEnable:  r.boolDefault(true, "tls_enable"),
		WSEnable:   r.boolDefault(true, "ws_enable"),
		WSSEnable:  r.boolDefault(true, "wss_enable"),
	}
	if cfg.Bridge.Type == "" {
		cfg.Bridge.Type = "both"
	}
	cfg.Bridge.PrimaryType = cfg.Bridge.Type
	if cfg.Bridge.PrimaryType == "both" {
		cfg.Bridge.PrimaryType = "tcp"
	}
	cfg.Bridge.ServerKCPEnabled = cfg.Bridge.KCPEnable && cfg.Network.BridgeKCPPort != 0 && (cfg.Bridge.Type == "kcp" || cfg.Bridge.Type == "udp" || cfg.Bridge.Type == "both")
	cfg.Bridge.ServerQUICEnabled = cfg.Bridge.QUICEnable && cfg.Network.BridgeQUICPort != 0 && (cfg.Bridge.Type == "quic" || cfg.Bridge.Type == "udp" || cfg.Bridge.Type == "both")
	cfg.Bridge.ServerTCPEnabled = cfg.Bridge.TCPEnable && cfg.Network.BridgeTCPPort != 0 && cfg.Bridge.PrimaryType == "tcp"
	cfg.Bridge.ServerTLSEnabled = cfg.Bridge.TLSEnable && cfg.Network.BridgeTLSPort != 0 && cfg.Bridge.PrimaryType == "tcp"
	cfg.Bridge.ServerWSEnabled = cfg.Bridge.WSEnable && cfg.Network.BridgeWSPort != 0 && cfg.Network.BridgePath != "" && cfg.Bridge.PrimaryType == "tcp"
	cfg.Bridge.ServerWSSEnabled = cfg.Bridge.WSSEnable && cfg.Network.BridgeWSSPort != 0 && cfg.Network.BridgePath != "" && cfg.Bridge.PrimaryType == "tcp"
	cfg.Bridge.Display = BridgeDisplayConfig{
		Path: r.stringDefault(cfg.Network.BridgePath, "bridge_show_path"),
		TCP: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerTCPEnabled, "bridge_tcp_show"),
			IP:   r.stringValue("bridge_tcp_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeTCPPort), "bridge_tcp_show_port"),
		},
		KCP: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerKCPEnabled, "bridge_kcp_show"),
			IP:   r.stringValue("bridge_kcp_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeKCPPort), "bridge_kcp_show_port"),
		},
		QUIC: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerQUICEnabled, "bridge_quic_show"),
			IP:   r.stringValue("bridge_quic_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeQUICPort), "bridge_quic_show_port"),
			ALPN: r.stringDefault(cfg.Network.QUICALPNList[0], "bridge_quic_show_alpn"),
		},
		TLS: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerTLSEnabled, "bridge_tls_show"),
			IP:   r.stringValue("bridge_tls_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeTLSPort), "bridge_tls_show_port"),
		},
		WS: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerWSEnabled, "bridge_ws_show"),
			IP:   r.stringValue("bridge_ws_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeWSPort), "bridge_ws_show_port"),
		},
		WSS: BridgeDisplayEndpoint{
			Show: r.boolDefault(cfg.Bridge.ServerWSSEnabled, "bridge_wss_show"),
			IP:   r.stringValue("bridge_wss_show_ip"),
			Port: r.stringDefault(portString(cfg.Network.BridgeWSSPort), "bridge_wss_show_port"),
		},
	}

	cfg.Proxy = ProxyConfig{
		AddOriginHeader:    r.boolDefault(false, "http_add_origin_header"),
		ResponseTimeout:    r.intDefault(100, "http_proxy_response_timeout"),
		ErrorPage:          r.stringDefault("web/static/page/error.html", "error_page"),
		ErrorPageTimeLimit: r.stringValue("error_page_time_limit"),
		ErrorPageFlowLimit: r.stringValue("error_page_flow_limit"),
		ErrorAlways:        r.boolDefault(false, "error_always"),
		BridgeHTTP3:        r.boolDefault(true, "bridge_http3"),
		ForceAutoSSL:       r.boolDefault(false, "force_auto_ssl"),
		SSL: SSLConfig{
			Email:           r.stringValue("ssl_email"),
			CA:              r.stringDefault("LetsEncrypt", "ssl_ca"),
			Path:            r.stringDefault("ssl", "ssl_path"),
			ZeroSSLAPI:      r.stringValue("ssl_zerossl_api"),
			DefaultCertFile: r.stringValue("https_default_cert_file"),
			DefaultKeyFile:  r.stringValue("https_default_key_file"),
			CacheMax:        r.intDefault(0, "ssl_cache_max"),
			CacheReload:     r.intDefault(0, "ssl_cache_reload"),
			CacheIdle:       r.intDefault(60, "ssl_cache_idle"),
		},
	}

	cfg.P2P = P2PConfig{
		STUNServers:         r.stringValue("p2p_stun_servers"),
		EnableUPNPPortmap:   r.boolDefault(false, "p2p_enable_upnp_portmap"),
		EnablePCPPortmap:    r.boolDefault(false, "p2p_enable_pcp_portmap"),
		EnableNATPMPPortmap: r.boolDefault(false, "p2p_enable_natpmp_portmap"),
		PortmapLeaseSeconds: r.intDefault(3600, "p2p_portmap_lease_seconds"),
		ProbeTimeoutMs:      r.intDefault(0, "p2p_probe_timeout_ms"),
		HandshakeTimeoutMs:  r.intDefault(0, "p2p_handshake_timeout_ms"),
		TransportTimeoutMs:  r.intDefault(0, "p2p_transport_timeout_ms"),
	}

	return cfg
}

func (r valueReader) stringValue(keys ...string) string {
	return r.stringDefault("", keys...)
}

func (r valueReader) stringDefault(defaultValue string, keys ...string) string {
	for _, key := range keys {
		value, err := lookupValue(r.values, key)
		if err != nil {
			continue
		}
		if text := stringifyValue(value); text != "" {
			return text
		}
	}
	return defaultValue
}

func (r valueReader) boolDefault(defaultValue bool, keys ...string) bool {
	for _, key := range keys {
		value, err := lookupValue(r.values, key)
		if err != nil {
			continue
		}
		if parsed, err := toBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func (r valueReader) intDefault(defaultValue int, keys ...string) int {
	for _, key := range keys {
		value, err := lookupValue(r.values, key)
		if err != nil {
			continue
		}
		if parsed, err := toInt(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func (r valueReader) int64Default(defaultValue int64, keys ...string) int64 {
	for _, key := range keys {
		value, err := lookupValue(r.values, key)
		if err != nil {
			continue
		}
		if parsed, err := toInt64(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func splitAndTrimCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func portString(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}
