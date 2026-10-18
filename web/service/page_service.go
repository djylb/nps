package service

import (
	"html/template"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
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
	Data map[string]interface{}
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
	case "file":
		data["name"] = "file server"
		data["type"] = "file"
	case "secret":
		data["name"] = "secret"
		data["type"] = "secret"
	case "p2p":
		data["name"] = "p2p"
		data["type"] = "p2p"
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
		if task.UserAuth == nil {
			data["auth"] = ""
		} else {
			data["auth"] = task.UserAuth.Content
		}
		data["name"] = "edit tunnel"
	case "hostlist":
		data["httpProxyPort"] = pageIntString(cfg.Network.HTTPProxyPort)
		data["httpsProxyPort"] = pageIntString(cfg.Network.HTTPSProxyPort)
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
	case "edit":
		client, err := s.clients().Get(pageInt(input, "id"))
		if err != nil {
			return PageBuildResult{}, ErrPageNotFound
		}
		data["c"] = client
		data["BlackIpList"] = strings.Join(client.BlackIpList, "\r\n")
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
		if global := s.globals().Get(); global != nil {
			data["globalBlackIpList"] = strings.Join(global.BlackIpList, "\r\n")
		}
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

func managementPageData(system SystemService, input PageBuildInput) map[string]interface{} {
	cfg := normalizePageConfig(input.Config)
	if system == nil {
		system = DefaultSystemService{}
	}
	info := system.Info()
	bridge := system.BridgeDisplay(cfg, input.Host)
	commonData := ui.ManagementPageCommonData{
		WebBaseURL:      cfg.Web.BaseURL,
		HeadCustomCode:  template.HTML(cfg.Web.HeadCustomCode),
		Version:         info.Version,
		Year:            info.Year,
		IsAdmin:         input.IsAdmin,
		ServerUpdate:    info.CanUpdate,
		Username:        strings.TrimSpace(input.Username),
		BridgeType:      bridge.Primary.Type,
		BridgeAddr:      bridge.Primary.Addr,
		BridgeIP:        bridge.Primary.IP,
		BridgePort:      bridge.Primary.Port,
		ProxyPort:       pageIntString(cfg.Network.HTTPProxyPort),
		ClientUpdateMin: version.RemoteUpdateMinVersion,
		Features: map[string]bool{
			"allow_user_login":           cfg.Feature.AllowUserLogin,
			"allow_flow_limit":           cfg.Feature.AllowFlowLimit,
			"allow_rate_limit":           cfg.Feature.AllowRateLimit,
			"allow_time_limit":           cfg.Feature.AllowTimeLimit,
			"allow_connection_num_limit": cfg.Feature.AllowConnectionNumLimit,
			"allow_multi_ip":             cfg.Feature.AllowMultiIP,
			"allow_tunnel_num_limit":     cfg.Feature.AllowTunnelNumLimit,
			"allow_local_proxy":          cfg.Feature.AllowLocalProxy,
			"allow_user_local":           cfg.Feature.AllowUserLocal,
			"allow_secret_link":          cfg.Feature.AllowSecretLink,
			"allow_user_change_username": cfg.Feature.AllowUserChangeUsername,
			"system_info_display":        cfg.Feature.SystemInfoDisplay,
		},
	}
	if common.IsWindows() {
		commonData.WindowsSuffix = ".exe"
	}
	if bridge.TCP.Enabled {
		commonData.TCPIP = bridge.TCP.IP
		commonData.TCPPort = bridge.TCP.Port
	}
	if bridge.KCP.Enabled {
		commonData.KCPIP = bridge.KCP.IP
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
