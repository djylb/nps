package servercfg

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/djylb/netx/forwarded"
	"github.com/djylb/nps/lib/auth"
)

type Snapshot struct {
	App      AppConfig
	Log      LogConfig
	Web      WebConfig
	Auth     AuthConfig
	Feature  FeatureConfig
	Security SecurityConfig
	Runtime  RuntimeConfig
	Webhook  WebhookConfig
	Network  NetworkConfig
	Bridge   BridgeConfig
	Proxy    ProxyConfig
	P2P      P2PConfig

	// reads holds, for each config key, what the reads of the key gave
	// while the snapshot was built, in order; a reload compares them.
	reads map[string][]string
	// Legacy holds a warning for each key of an earlier version the config
	// sets (legacyKeys).
	Legacy []string
}

type AppConfig struct {
	Name        string
	Timezone    string
	DNSServer   string
	PprofIP     string
	PprofPort   string
	NTPServer   string
	NTPInterval int
	// GeoIPPath and GeoSitePath are the geodata files of the access lists,
	// relative to the run path unless absolute.
	GeoIPPath   string
	GeoSitePath string
	// AclMaxRules limits the rules of each access list users may write;
	// 0 means no limit.
	AclMaxRules int
	// IPRegisterMaxHours bounds registrations made on the registration page
	// with the public key, and with the key of a client that sets no limit.
	IPRegisterMaxHours int
	// IPRegisterIP and IPRegisterPort, when the port is set, serve the
	// registration page on a listener of its own.
	IPRegisterIP   string
	IPRegisterPort int
	// IPRegisterCertFile and IPRegisterKeyFile, when both are set, serve
	// the listener of the registration page over TLS.
	IPRegisterCertFile string
	IPRegisterKeyFile  string
}

type LogConfig struct {
	Type         string // stdout, file, both, off
	Format       string // auto (pretty on a terminal, else logfmt), text or json
	Level        string // "info,bridge=debug"
	Path         string
	Security     string // security log file; empty for none
	MaxFiles     int
	MaxDays      int
	MaxSize      int // MB
	Compress     bool
	Memory       int    // entries kept for the log viewer of the web manager; 0 for none
	Secrets      bool   // write keys and passwords in lines, for development
	Instance     string // instance= on every line, to tell instances apart where logs meet
	Access       string // access log file of the domain proxy; empty for none
	AccessFormat string // combined or json
}

type WebConfig struct {
	InstanceKeyFile string
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
	// TrustedProxies is TrustedProxyIPs parsed.
	TrustedProxies *forwarded.PrefixSet
}

type FeatureConfig struct {
	AllowUserLogin          bool
	AllowUserRegister       bool
	AllowUserVkeyLogin      bool
	AllowUserChangeUsername bool
	OpenCaptcha             bool
	AllowFlowLimit          bool
	// FlowLimitActual makes flow limits count the actual usage, twice the
	// inbound and outbound traffic, rather than the traffic itself.
	FlowLimitActual         bool
	AllowRateLimit          bool
	AllowTimeLimit          bool
	AllowConnectionNumLimit bool
	AllowMultiIP            bool
	AllowTunnelNumLimit     bool
	AllowLocalProxy         bool
	AllowUserLocal          bool
	AllowSecretLocal        bool
	// LegacySecretLink is allow_secret_link of earlier versions, read in
	// legacy.go: secret tunnels without a destination list let visitors
	// name any destination. It goes with the next major version.
	LegacySecretLink  bool
	SystemInfoDisplay bool
	AllowPorts        string
}

type SecurityConfig struct {
	SecureMode        bool
	ForcePoW          bool
	AuthTimeWindow    int64
	PoWBits           int
	LoginBanTime      int64
	LoginIPBanTime    int64
	LoginUserBanTime  int64
	LoginMaxFailTimes int
	LoginMaxBody      int64
	LoginMaxSkew      int64
	LoginACLMode      int // 0 off, 1 whitelist, 2 blacklist
	LoginACLRules     string
}

type RuntimeConfig struct {
	ClientHistoryLimit int
	PublicVKey         string
	VisitorVKey        string
	DisconnectTimeout  int
	// DeadS and IdleS are dead_s and idle_s of the new protocol's sessions
	// (§5, 0: not set): idle from session_idle_s, dead only from a
	// disconnect_timeout the config names.
	DeadS, IdleS      int
	FlowStoreInterval int
}

// WebhookConfig configures the webhook; an empty URL turns it off.
type WebhookConfig struct {
	URL            string
	Secret         string
	TimeoutSeconds int
	Retries        int
	QueueSize      int
	// ReportInterval is the seconds between full reports; 0 turns them off.
	ReportInterval int
}

type NetworkConfig struct {
	BridgeIP           string
	BridgeHost         string
	BridgeTCPIP        string
	BridgeKCPIP        string
	BridgeQUICIP       string
	BridgeTLSIP        string
	BridgeWSIP         string
	BridgeWSSIP        string
	BridgePort         int
	BridgeTCPPort      int
	BridgeKCPPort      int
	BridgeQUICPort     int
	BridgeTLSPort      int
	BridgeWSPort       int
	BridgeWSSPort      int
	BridgePath         string
	BridgeTrustedIPs   string
	BridgeRealIPHeader string
	// NewProtocol turns on the core protocol on the tcp, tls, ws and wss
	// bridge ports (nps.conf new_protocol), off by default. Temporary:
	// removed once npc has moved to the core protocol.
	NewProtocol bool
	// NewProtocolTLSALPN is the ALPN list of the TLS bridge with NewProtocol
	// (nps.conf new_protocol_tls_alpn, comma-separated); empty selects none.
	NewProtocolTLSALPN []string
	// NewProtocolWSPaths are the WebSocket paths whose requests go to the
	// core protocol whatever their subprotocol (nps.conf
	// new_protocol_ws_paths, comma-separated, default none). On BridgePath
	// the subprotocol decides: binary is the core protocol, none the old one.
	NewProtocolWSPaths []string
	// MixPort is the mixed port (nps.conf mix_port, 0 off; TCP and UDP on
	// the same number), MixIP its address (mix_ip, default bridge_ip).
	// MixPlain lets it take plaintext carriers (mix_plain, default off);
	// MixLegacy the old protocol where the first bytes tell it (mix_legacy,
	// default on), which also applies to bridge://mix.
	MixIP     string
	MixPort   int
	MixPlain  bool
	MixLegacy bool
	// ClientMaxPipes limits the pipes of one client over all its sessions
	// (client_max_pipes, 0 unlimited).
	ClientMaxPipes int
	// BridgeGeneric: the config names only the generic bridge_port, which
	// is then a full mixed port (see legacy.go).
	BridgeGeneric bool
	// CompressMaxWindow and CompressMaxRatio are the receive limits of
	// compressed links on the core protocol (nps.conf compress_max_window
	// in bytes, compress_max_ratio; 0 is the default, a negative ratio
	// disables that check).
	CompressMaxWindow uint64
	CompressMaxRatio  int64
	HTTPProxyIP       string
	HTTPProxyPort     int
	HTTPSProxyPort    int
	// HTTPProxyAccessPort and HTTPSProxyAccessPort are the ports visitors
	// reach the host proxy on from outside, behind a reverse proxy or port
	// mapping; they default to the listen ports and only build links.
	HTTPProxyAccessPort    int
	HTTPSProxyAccessPort   int
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
	// Fallback404File is the body of the 404 that bridge connections of no
	// known route get (nps.conf fallback_404_file); empty selects a minimal
	// built-in body.
	Fallback404File string
	BridgeHTTP3     bool
	ForceAutoSSL    bool
	SSL             SSLConfig
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

// valueReader reads the config keys of a snapshot. Every key the snapshot
// is built from is read through it, so that it records the keys nps knows
// (Keys) and, for each, what its reads gave: a value that depends on other
// keys, such as one defaulting to another, is recorded under every key it
// was read with.
type valueReader struct {
	values map[string]any
	reads  map[string][]string
}

func (r *valueReader) record(result any, keys []string) {
	text := fmt.Sprint(result)
	for _, key := range keys {
		r.reads[key] = append(r.reads[key], text)
	}
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
	r := &valueReader{values: values, reads: map[string][]string{}}
	cfg := &Snapshot{reads: r.reads, Legacy: legacyWarnings(values)}

	cfg.App = AppConfig{
		Name:               r.stringDefault("nps", "appname"),
		Timezone:           r.stringValue("timezone"),
		DNSServer:          r.stringValue("dns_server"),
		PprofIP:            r.stringValue("pprof_ip"),
		PprofPort:          r.stringDefault("0", "pprof_port"),
		NTPServer:          r.stringValue("ntp_server"),
		NTPInterval:        r.intDefault(5, "ntp_interval"),
		GeoIPPath:          r.stringDefault("conf/geoip.dat", "geoip_path"),
		GeoSitePath:        r.stringDefault("conf/geosite.dat", "geosite_path"),
		AclMaxRules:        r.intDefault(1000, "acl_max_rules"),
		IPRegisterMaxHours: r.intDefault(2, "ip_register_max_hours"),
		IPRegisterIP:       r.stringDefault("0.0.0.0", "ip_register_ip"),
		IPRegisterPort:     r.intDefault(0, "ip_register_port"),
		IPRegisterCertFile: r.stringValue("ip_register_cert_file"),
		IPRegisterKeyFile:  r.stringValue("ip_register_key_file"),
	}

	cfg.Log = LogConfig{
		Type:         r.stringDefault("stdout", "log"),
		Format:       r.stringDefault("auto", "log_format"),
		Level:        r.stringDefault("info", "log_level"),
		Path:         r.stringValue("log_path"),
		Security:     r.stringValue("security_log"),
		MaxFiles:     r.intDefault(30, "log_max_files"),
		MaxDays:      r.intDefault(30, "log_max_days"),
		MaxSize:      r.intDefault(5, "log_max_size"),
		Compress:     r.boolDefault(false, "log_compress"),
		Memory:       r.intDefault(1000, "log_memory"),
		Secrets:      r.boolDefault(false, "log_secrets"),
		Instance:     r.stringValue("log_instance"),
		Access:       r.stringValue("access_log"),
		AccessFormat: r.stringDefault("combined", "access_log_format"),
	}

	cfg.Web = WebConfig{
		InstanceKeyFile: r.stringValue("instance_key_file"),
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
	cfg.Auth.TrustedProxies = auth.ParseTrustedProxies(cfg.Auth.TrustedProxyIPs)

	cfg.Feature = FeatureConfig{
		AllowUserLogin:          r.boolDefault(false, "allow_user_login"),
		AllowUserRegister:       r.boolDefault(false, "allow_user_register"),
		AllowUserChangeUsername: r.boolDefault(false, "allow_user_change_username"),
		OpenCaptcha:             r.boolDefault(false, "open_captcha"),
		AllowFlowLimit:          r.boolDefault(false, "allow_flow_limit"),
		FlowLimitActual:         r.stringDefault("total", "flow_limit_type") == "actual",
		AllowRateLimit:          r.boolDefault(false, "allow_rate_limit"),
		AllowTimeLimit:          r.boolDefault(false, "allow_time_limit"),
		AllowConnectionNumLimit: r.boolDefault(false, "allow_connection_num_limit"),
		AllowMultiIP:            r.boolDefault(false, "allow_multi_ip"),
		AllowTunnelNumLimit:     r.boolDefault(false, "allow_tunnel_num_limit"),
		AllowLocalProxy:         r.boolDefault(false, "allow_local_proxy"),
		AllowSecretLocal:        r.boolDefault(false, "allow_secret_local"),
		SystemInfoDisplay:       r.boolDefault(false, "system_info_display"),
		AllowPorts:              r.stringValue("allow_ports"),
	}
	cfg.Feature.LegacySecretLink = legacySecretLink(r)
	cfg.Feature.AllowUserLocal = r.boolDefault(cfg.Feature.AllowLocalProxy, "allow_user_local")
	cfg.Feature.AllowUserVkeyLogin = r.boolDefault(cfg.Feature.AllowUserLogin, "allow_user_vkey_login")

	cfg.Security = SecurityConfig{
		SecureMode:        r.boolDefault(false, "secure_mode"),
		AuthTimeWindow:    r.int64Default(300, "auth_time_window"),
		ForcePoW:          r.boolDefault(false, "force_pow"),
		PoWBits:           r.intDefault(20, "pow_bits"),
		LoginBanTime:      r.int64Default(5, "login_ban_time"),
		LoginIPBanTime:    r.int64Default(180, "login_ip_ban_time"),
		LoginUserBanTime:  r.int64Default(3600, "login_user_ban_time"),
		LoginMaxFailTimes: r.intDefault(10, "login_max_fail_times"),
		LoginMaxBody:      r.int64Default(1024, "login_max_body"),
		LoginMaxSkew:      r.int64Default(5*60*1000, "login_max_skew"),
		LoginACLMode:      r.intDefault(0, "login_acl_mode"),
		LoginACLRules:     r.stringValue("login_acl_rules"),
	}

	if cfg.Security.AuthTimeWindow < 0 {
		cfg.Security.AuthTimeWindow = 300
	}

	cfg.Runtime = RuntimeConfig{
		ClientHistoryLimit: r.intDefault(5, "client_history_limit"),
		PublicVKey:         r.stringValue("public_vkey"),
		VisitorVKey:        r.stringValue("visitor_vkey"),
		DisconnectTimeout:  r.intDefault(30, "disconnect_timeout"),
		FlowStoreInterval:  r.intDefault(0, "flow_store_interval"),
	}

	cfg.Webhook = WebhookConfig{
		URL:            r.stringValue("webhook_url"),
		Secret:         r.stringValue("webhook_secret"),
		TimeoutSeconds: r.intDefault(5, "webhook_timeout"),
		Retries:        max(r.intDefault(3, "webhook_retries"), 0),
		QueueSize:      r.intDefault(1024, "webhook_queue_size"),
		ReportInterval: max(r.intDefault(60, "webhook_report_interval"), 0),
	}
	if cfg.Webhook.TimeoutSeconds <= 0 {
		cfg.Webhook.TimeoutSeconds = 5
	}
	if cfg.Webhook.QueueSize <= 0 {
		cfg.Webhook.QueueSize = 1024
	}

	if cfg.Runtime.ClientHistoryLimit < 0 {
		cfg.Runtime.ClientHistoryLimit = 5
	}

	cfg.Network.BridgeIP = r.stringDefault(r.stringDefault("0.0.0.0", "bridge_tcp_ip"), "bridge_ip")
	cfg.Network.BridgeHost = r.stringValue("bridge_host")
	cfg.Network.NewProtocol = r.boolDefault(false, "new_protocol")
	if _, err := lookupValue(r.values, "disconnect_timeout"); err == nil {
		cfg.Runtime.DeadS = max(cfg.Runtime.DisconnectTimeout, 0)
	}
	cfg.Runtime.IdleS = max(r.intDefault(0, "session_idle_s"), 0)
	cfg.Network.CompressMaxWindow = uint64(max(r.intDefault(0, "compress_max_window"), 0))
	cfg.Network.CompressMaxRatio = int64(r.intDefault(0, "compress_max_ratio"))
	for v := range strings.SplitSeq(r.stringValue("new_protocol_tls_alpn"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			cfg.Network.NewProtocolTLSALPN = append(cfg.Network.NewProtocolTLSALPN, v)
		}
	}
	for v := range strings.SplitSeq(r.stringValue("new_protocol_ws_paths"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			cfg.Network.NewProtocolWSPaths = append(cfg.Network.NewProtocolWSPaths, v)
		}
	}
	cfg.Network.BridgeTCPIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_tcp_ip")
	cfg.Network.BridgeKCPIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_kcp_ip")
	cfg.Network.BridgeQUICIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_quic_ip")
	cfg.Network.BridgeTLSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_tls_ip")
	cfg.Network.BridgeWSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_ws_ip")
	cfg.Network.BridgeWSSIP = r.stringDefault(cfg.Network.BridgeIP, "bridge_wss_ip")
	cfg.Network.MixIP = r.stringDefault(cfg.Network.BridgeIP, "mix_ip")
	cfg.Network.MixPort = r.intDefault(0, "mix_port")
	cfg.Network.MixPlain = r.boolDefault(false, "mix_plain")
	cfg.Network.MixLegacy = r.boolDefault(true, "mix_legacy")
	cfg.Network.ClientMaxPipes = max(r.intDefault(0, "client_max_pipes"), 0)
	cfg.Network.BridgePort = r.intDefault(r.intDefault(0, "bridge_tcp_port"), "bridge_port")
	cfg.Network.BridgeTCPPort = r.intDefault(cfg.Network.BridgePort, "bridge_tcp_port")
	cfg.Network.BridgeKCPPort = r.intDefault(cfg.Network.BridgePort, "bridge_kcp_port")
	cfg.Network.BridgeQUICPort = r.intDefault(0, "bridge_quic_port")
	cfg.Network.BridgeTLSPort = r.intDefault(0, names("bridge_tls_port")...)
	cfg.Network.BridgeWSPort = r.intDefault(0, "bridge_ws_port")
	cfg.Network.BridgeWSSPort = r.intDefault(0, "bridge_wss_port")
	cfg.Network.BridgePath = r.stringDefault("/ws", "bridge_path")
	cfg.Network.BridgeTrustedIPs = r.stringValue("bridge_trusted_ips")
	cfg.Network.BridgeRealIPHeader = r.stringValue("bridge_real_ip_header")
	cfg.Network.HTTPProxyIP = r.stringDefault("0.0.0.0", "http_proxy_ip")
	cfg.Network.HTTPProxyPort = r.intDefault(0, "http_proxy_port")
	cfg.Network.HTTPSProxyPort = r.intDefault(0, "https_proxy_port")
	cfg.Network.HTTPProxyAccessPort = r.intDefault(cfg.Network.HTTPProxyPort, "http_proxy_access_port")
	cfg.Network.HTTPSProxyAccessPort = r.intDefault(cfg.Network.HTTPSProxyPort, "https_proxy_access_port")
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
	legacyGenericPort(r, cfg)
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
		Fallback404File:    r.stringValue("fallback_404_file"),
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

func (r *valueReader) stringValue(keys ...string) string {
	return r.stringDefault("", keys...)
}

func (r *valueReader) stringDefault(defaultValue string, keys ...string) (result string) {
	defer func() { r.record(result, keys) }()
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

func (r *valueReader) boolDefault(defaultValue bool, keys ...string) (result bool) {
	defer func() { r.record(result, keys) }()
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

func (r *valueReader) intDefault(defaultValue int, keys ...string) (result int) {
	defer func() { r.record(result, keys) }()
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

func (r *valueReader) int64Default(defaultValue int64, keys ...string) (result int64) {
	defer func() { r.record(result, keys) }()
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

// Keys returns the config keys nps knows, sorted: those the snapshot is
// built from, the alternatives of a key included.
func Keys() []string {
	keys := make([]string, 0, len(defaultSnapshot.reads))
	for key := range defaultSnapshot.reads {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
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
