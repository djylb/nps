package service

import (
	"encoding/hex"
	"html/template"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/instancekey"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/ui"
)

type PageService interface {
	Build(PageBuildInput) (PageBuildResult, error)
}

type DefaultPageService struct {
	Clients ClientService
	Globals GlobalService
	Index   IndexService
	System  SystemService
}

type PageBuildInput struct {
	Config     *servercfg.Snapshot
	Controller string
	Action     string
	Host       string
	IsAdmin    bool
	Username   string
	Params     map[string]string
}

type PageBuildResult struct {
	Data map[string]any
}

func (s DefaultPageService) Build(input PageBuildInput) (PageBuildResult, error) {
	switch strings.TrimSpace(input.Controller) {
	case "index":
		return s.buildIndex(input)
	case "client":
		return s.buildClient(input)
	case "global":
		return s.buildGlobal(input)
	default:
		return PageBuildResult{}, ErrPageNotFound
	}
}

func (s DefaultPageService) buildIndex(input PageBuildInput) (PageBuildResult, error) {
	cfg := normalizePageConfig(input.Config)
	data := managementPageData(s.system(), input)
	switch strings.TrimSpace(input.Action) {
	case "index":
		data["data"] = s.index().DashboardData(true)
		data["name"] = "dashboard"
	case "help":
		data["name"] = "about"
	case "tcp":
		data["name"] = "tcp"
		data["type"] = "tcp"
	case "udp":
		data["name"] = "udp"
		data["type"] = "udp"
	case "socks5":
		data["name"] = "socks5"
		data["type"] = "socks5"
	case "http":
		data["name"] = "http proxy"
		data["type"] = "httpProxy"
	case "mix":
		data["name"] = "mix proxy"
		data["type"] = "mixProxy"
	case "service":
		data["name"] = "virtual backend"
		data["type"] = "service"
	case "secret":
		data["name"] = "secret"
		data["type"] = "secret"
	case "host":
		data["name"] = "host"
		data["type"] = "hostServer"
	case "all":
		clientID := pageParam(input, "client_id")
		data["client_id"] = clientID
		data["name"] = "client id:" + clientID
	case "add":
		data["type"] = pageParam(input, "type")
		data["client_id"] = pageParam(input, "client_id")
		data["name"] = "add tunnel"
	case "edit":
		task, err := s.index().GetTunnel(pageInt(input, "id"))
		if err != nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		data["t"] = task
		setAccessListData(data, &task.SourceLists)
		if task.UserAuth == nil {
			data["auth"] = ""
		} else {
			data["auth"] = task.UserAuth.Content
		}
		data["name"] = "edit tunnel"
	case "launch":
		task, err := s.index().GetTunnel(pageInt(input, "id"))
		if err != nil || task.Mode != "secret" || task.Client == nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		vkey, _ := data["visitor_vkey"].(string)
		if vkey == "" {
			vkey = task.Client.VerifyKey
		}
		target := ""
		if task.Target != nil {
			target, _, _ = strings.Cut(strings.TrimSpace(task.Target.TargetStr), "\n")
		}
		data["launch_ctx"] = map[string]any{"kind": "tunnel", "id": task.Id, "vkey": vkey, "password": task.Password,
			"defaultTarget": target, "form": task.LaunchForm, "save": "index/savelaunch"}
		data["name"] = "start command"
	case "hostlist":
		data["httpAccessPort"] = cfg.Network.HTTPProxyAccessPort
		data["httpsAccessPort"] = cfg.Network.HTTPSProxyAccessPort
		data["httpsEnabled"] = cfg.Network.HTTPSProxyPort > 0
		data["client_id"] = pageParam(input, "client_id")
		data["name"] = "host list"
	case "addhost":
		data["client_id"] = pageParam(input, "client_id")
		data["name"] = "add host"
	case "edithost":
		host, err := s.index().GetHost(pageInt(input, "id"))
		if err != nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		data["h"] = host
		setAccessListData(data, &host.SourceLists)
		if host.UserAuth == nil {
			data["auth"] = ""
		} else {
			data["auth"] = host.UserAuth.Content
		}
		data["name"] = "edit"
	default:
		return PageBuildResult{}, ErrPageNotFound
	}
	return PageBuildResult{Data: data}, nil
}

func (s DefaultPageService) buildClient(input PageBuildInput) (PageBuildResult, error) {
	data := managementPageData(s.system(), input)
	switch strings.TrimSpace(input.Action) {
	case "list":
		data["name"] = "client"
	case "add":
		data["name"] = "add client"
	case "launch":
		client, err := s.clients().Get(pageInt(input, "id"))
		if err != nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		data["launch_ctx"] = map[string]any{"kind": "client", "id": client.Id, "vkey": client.VerifyKey,
			"form": client.LaunchForm, "save": "client/savelaunch"}
		data["name"] = "start command"
	case "edit":
		client, err := s.clients().Get(pageInt(input, "id"))
		if err != nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		data["c"] = client
		setAccessListData(data, &client.SourceLists)
		data["name"] = "edit client"
	default:
		return PageBuildResult{}, ErrPageNotFound
	}
	return PageBuildResult{Data: data}, nil
}

func (s DefaultPageService) buildGlobal(input PageBuildInput) (PageBuildResult, error) {
	data := managementPageData(s.system(), input)
	switch strings.TrimSpace(input.Action) {
	case "index":
		data["name"] = "global"
		data["globalNowConn"], _ = model.GlobalConnections()
		data["globalIpListPriority"] = acl.DenyFirst
		if global := s.globals().Get(); global != nil {
			data["globalIpListPriority"] = global.IpListPriority
			data["globalBlackIpList"] = joinRuleLines(global.BlackIpList)
			data["globalWhiteIpList"] = joinRuleLines(global.WhiteIpList)
			data["globalRateLimit"] = global.RateLimit
			data["globalMaxConn"] = global.MaxConn
		}
		data["geo"] = s.system().GeoDataStatus()
	case "banlist":
		data["name"] = "banlist"
	default:
		return PageBuildResult{}, ErrPageNotFound
	}
	return PageBuildResult{Data: data}, nil
}

func (s DefaultPageService) clients() ClientService {
	if s.Clients != nil {
		return s.Clients
	}
	return DefaultClientService{}
}

func (s DefaultPageService) globals() GlobalService {
	if s.Globals != nil {
		return s.Globals
	}
	return DefaultGlobalService{}
}

func (s DefaultPageService) index() IndexService {
	if s.Index != nil {
		return s.Index
	}
	return DefaultIndexService{}
}

func (s DefaultPageService) system() SystemService {
	if s.System != nil {
		return s.System
	}
	return DefaultSystemService{}
}

// pageAdmitKey is the admission key for the links of an administrator's pages.
func pageAdmitKey(cfg *servercfg.Snapshot, admin bool) string {
	if !admin || cfg.Web.InstanceKeyFile == "" {
		return ""
	}
	key, err := instancekey.AdmitKey(cfg.Web.InstanceKeyFile, cfg.Web.AdmitKey)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(key)
}

func managementPageData(system SystemService, input PageBuildInput) map[string]any {
	cfg := normalizePageConfig(input.Config)
	if system == nil {
		system = DefaultSystemService{}
	}
	info := system.Info()
	bridge := system.BridgeDisplay(cfg, input.Host)
	commonData := ui.ManagementPageCommonData{
		WebBaseURL:     cfg.Web.BaseURL,
		HeadCustomCode: template.HTML(cfg.Web.HeadCustomCode),
		Version:        info.Version,
		Year:           info.Year,
		IsAdmin:        input.IsAdmin,
		AdmitKey:       pageAdmitKey(cfg, input.IsAdmin),
		MixIP:          bridge.Primary.IP,
		MixPort:        pageIntString(cfg.Network.MixPort),
		ServerUpdate:   info.CanUpdate,
		Username:       strings.TrimSpace(input.Username),
		BridgeType:     bridge.Primary.Type,
		VisitorVKey:    cfg.Runtime.VisitorVKey,
		BridgeAddr:     bridge.Primary.Addr,
		BridgeIP:       bridge.Primary.IP,
		BridgePort:     bridge.Primary.Port,
		ProxyPort:      pageIntString(cfg.Network.HTTPProxyPort),
		Features: map[string]bool{
			"allow_user_login":           cfg.Feature.AllowUserLogin,
			"allow_flow_limit":           cfg.Feature.AllowFlowLimit,
			"flow_limit_actual":          cfg.Feature.FlowLimitActual,
			"allow_rate_limit":           cfg.Feature.AllowRateLimit,
			"allow_time_limit":           cfg.Feature.AllowTimeLimit,
			"allow_connection_num_limit": cfg.Feature.AllowConnectionNumLimit,
			"allow_multi_ip":             cfg.Feature.AllowMultiIP,
			"allow_tunnel_num_limit":     cfg.Feature.AllowTunnelNumLimit,
			"allow_local_proxy":          cfg.Feature.AllowLocalProxy,
			"allow_user_local":           cfg.Feature.AllowUserLocal,
			"allow_user_change_username": cfg.Feature.AllowUserChangeUsername,
			"system_info_display":        cfg.Feature.SystemInfoDisplay,
		},
	}
	if runenv.IsWindows() {
		commonData.WindowsSuffix = ".exe"
	}
	if bridge.TCP.Enabled {
		commonData.TCPIP = bridge.TCP.IP
		commonData.TCPPort = bridge.TCP.Port
	}
	if bridge.KCP.Enabled {
		commonData.KCPPort = bridge.KCP.Port
	}
	if bridge.TLS.Enabled {
		commonData.TLSIP = bridge.TLS.IP
		commonData.TLSPort = bridge.TLS.Port
	}
	if bridge.QUIC.Enabled {
		commonData.QUICIP = bridge.QUIC.IP
		commonData.QUICPort = bridge.QUIC.Port
		commonData.QUICALPN = bridge.QUIC.ALPN
		commonData.QUICAddr = bridge.QUIC.Addr
	}
	if bridge.Path != "" {
		commonData.WSPath = bridge.Path
	}
	if bridge.WS.Enabled {
		commonData.WSIP = bridge.WS.IP
		commonData.WSPort = bridge.WS.Port
	}
	if bridge.WSS.Enabled {
		commonData.WSSIP = bridge.WSS.IP
		commonData.WSSPort = bridge.WSS.Port
	}
	return commonData.Map()
}

// setAccessListData exposes the lists of an edited entry to its template.
func setAccessListData(data map[string]any, l *model.SourceLists) {
	data["RegisteredOnly"] = l.RegisteredOnly
	data["IpListPriority"] = l.IpListPriority
	data["BlackIpList"] = joinRuleLines(l.BlackIpList)
	data["WhiteIpList"] = joinRuleLines(l.WhiteIpList)
}

func normalizePageConfig(cfg *servercfg.Snapshot) *servercfg.Snapshot {
	if cfg != nil {
		return cfg
	}
	return &servercfg.Snapshot{}
}

func pageParam(input PageBuildInput, key string) string {
	if input.Params == nil {
		return ""
	}
	return strings.TrimSpace(input.Params[key])
}

func pageInt(input PageBuildInput, key string) int {
	value, err := strconv.Atoi(pageParam(input, key))
	if err != nil {
		return 0
	}
	return value
}

func pageIntString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}
