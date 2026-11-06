package api

import "github.com/djylb/nps/web/ui"

type BootstrapPayload struct {
	Status     int                 `json:"status"`
	App        BootstrapApp        `json:"app"`
	Session    BootstrapSession    `json:"session"`
	Actor      *Actor              `json:"actor"`
	Pages      []PageEntry         `json:"pages"`
	Actions    []ActionEntry       `json:"actions"`
	Features   BootstrapFeatures   `json:"features"`
	Security   BootstrapSecurity   `json:"security"`
	Routes     BootstrapRoutes     `json:"routes"`
	UI         BootstrapUI         `json:"ui"`
	Extensions BootstrapExtensions `json:"extensions"`
	Request    RequestMetadata     `json:"request"`
	PublicKey  string              `json:"public_key,omitempty"`
}

type BootstrapApp struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Year           int    `json:"year"`
	WebBaseURL     string `json:"web_base_url"`
	HeadCustomCode string `json:"head_custom_code"`
}

type BootstrapSession struct {
	Authenticated bool   `json:"authenticated"`
	IsAdmin       bool   `json:"is_admin"`
	Username      string `json:"username,omitempty"`
	ClientID      *int   `json:"client_id"`
	ClientIDs     []int  `json:"client_ids,omitempty"`
	SubjectID     string `json:"subject_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Kind          string `json:"kind,omitempty"`
}

type BootstrapFeatures struct {
	AllowUserLogin          bool `json:"allow_user_login"`
	AllowUserRegister       bool `json:"allow_user_register"`
	AllowUserVkeyLogin      bool `json:"allow_user_vkey_login"`
	AllowFlowLimit          bool `json:"allow_flow_limit"`
	AllowRateLimit          bool `json:"allow_rate_limit"`
	AllowTimeLimit          bool `json:"allow_time_limit"`
	AllowConnectionNumLimit bool `json:"allow_connection_num_limit"`
	AllowMultiIP            bool `json:"allow_multi_ip"`
	AllowTunnelNumLimit     bool `json:"allow_tunnel_num_limit"`
	AllowLocalProxy         bool `json:"allow_local_proxy"`
	AllowUserLocal          bool `json:"allow_user_local"`
	AllowSecretLink         bool `json:"allow_secret_link"`
	AllowUserChangeUsername bool `json:"allow_user_change_username"`
	SystemInfoDisplay       bool `json:"system_info_display"`
	OpenCaptcha             bool `json:"open_captcha"`
}

type BootstrapSecurity struct {
	SecureMode bool `json:"secure_mode"`
	ForcePoW   bool `json:"force_pow"`
	PoWBits    int  `json:"pow_bits"`
}

type BootstrapRoutes struct {
	Page              string `json:"page"`
	Render            string `json:"render"`
	AppShell          string `json:"app_shell"`
	StaticBase        string `json:"static_base"`
	AppAssetsBase     string `json:"app_assets_base"`
	CaptchaNew        string `json:"captcha_new"`
	CaptchaBase       string `json:"captcha_base"`
	APIBase           string `json:"api_base"`
	ManagementAPIBase string `json:"management_api_base"`
	APIAuth           string `json:"api_auth"`
	APILogin          string `json:"api_login"`
	APICaptcha        string `json:"api_captcha"`
	APIGetAuthKey     string `json:"api_get_auth_key"`
	APIGetTime        string `json:"api_get_time"`
	APIGetCert        string `json:"api_get_cert"`
	GetAuthKey        string `json:"get_auth_key"`
	GetTime           string `json:"get_time"`
	GetCert           string `json:"get_cert"`
	Login             string `json:"login"`
	Logout            string `json:"logout"`
}

type BootstrapUI struct {
	Mode                string                   `json:"mode"`
	ManagementShell     string                   `json:"management_shell"`
	LegacyPagesEnabled  bool                     `json:"legacy_pages_enabled"`
	SPAFallbackEnabled  bool                     `json:"spa_fallback_enabled"`
	ReactBootstrapReady bool                     `json:"react_bootstrap_ready"`
	ShellAssetsReady    bool                     `json:"shell_assets_ready"`
	ShellAssets         ui.ManagementShellAssets `json:"shell_assets"`
}

type BootstrapExtensions struct {
	Authorization BootstrapAuthorizationExtension `json:"authorization"`
	Cluster       BootstrapClusterExtension       `json:"cluster"`
}

type BootstrapAuthorizationExtension struct {
	Roles               []string            `json:"roles,omitempty"`
	Permissions         []string            `json:"permissions,omitempty"`
	ClientIDs           []int               `json:"client_ids,omitempty"`
	KnownRoles          []string            `json:"known_roles,omitempty"`
	KnownPermissions    []string            `json:"known_permissions,omitempty"`
	ManagementAdmin     string              `json:"management_admin"`
	ResourcePermissions map[string][]string `json:"resource_permissions,omitempty"`
}

type BootstrapClusterExtension struct {
	NodeID         string `json:"node_id,omitempty"`
	EventsEnabled  bool   `json:"events_enabled"`
	CallbacksReady bool   `json:"callbacks_ready"`
}
