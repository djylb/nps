package service

import (
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
)

type IndexService interface {
	DashboardData(force bool) map[string]interface{}
	ListTunnels(TunnelListInput) ([]*file.Tunnel, int)
	AddTunnel(AddTunnelInput) (TunnelMutation, error)
	GetTunnel(id int) (*file.Tunnel, error)
	EditTunnel(EditTunnelInput) (TunnelMutation, error)
	StopTunnel(id int, mode string) error
	DeleteTunnel(id int) error
	StartTunnel(id int, mode string) error
	ClearTunnel(id int, mode string) error
	ListHosts(HostListInput) ([]*file.Host, int)
	GetHost(id int) (*file.Host, error)
	DeleteHost(id int) error
	StartHost(id int, mode string) error
	StopHost(id int, mode string) error
	ClearHost(id int, mode string) error
	AddHost(AddHostInput) (HostMutation, error)
	EditHost(EditHostInput) (HostMutation, error)
}

type DefaultIndexService struct {
	Backend Backend
}

type TunnelListInput struct {
	Offset   int
	Limit    int
	Type     string
	ClientID int
	Search   string
	Sort     string
	Order    string
}

type HostListInput struct {
	Offset   int
	Limit    int
	ClientID int
	Search   string
	Sort     string
	Order    string
}

type TunnelMutation struct {
	ID       int
	ClientID int
	Mode     string
}

type HostMutation struct {
	ID       int
	ClientID int
}

type AddTunnelInput struct {
	IsAdmin        bool
	AllowUserLocal bool
	ClientID       int
	Port           int
	ServerIP       string
	Mode           string
	TargetType     string
	Target         string
	ProxyProtocol  int
	LocalProxy     bool
	Auth           string
	Remark         string
	Password       string
	LocalPath      string
	StripPre       string
	EnableHTTP     bool
	EnableSocks5   bool
	DestACLMode    int
	DestACLRules   string
	FlowLimit      int64
	TimeLimit      string
}

type EditTunnelInput struct {
	ID             int
	IsAdmin        bool
	AllowUserLocal bool
	ClientID       int
	Port           int
	ServerIP       string
	Mode           string
	TargetType     string
	Target         string
	ProxyProtocol  int
	LocalProxy     bool
	Auth           string
	Remark         string
	Password       string
	LocalPath      string
	StripPre       string
	EnableHTTP     bool
	EnableSocks5   bool
	DestACLMode    int
	DestACLRules   string
	FlowLimit      int64
	TimeLimit      string
	ResetFlow      bool
}

type AddHostInput struct {
	IsAdmin        bool
	AllowUserLocal bool
	ClientID       int
	Host           string
	Target         string
	ProxyProtocol  int
	LocalProxy     bool
	Auth           string
	Header         string
	RespHeader     string
	HostChange     string
	Remark         string
	Location       string
	PathRewrite    string
	RedirectURL    string
	FlowLimit      int64
	TimeLimit      string
	Scheme         string
	HTTPSJustProxy bool
	TLSOffload     bool
	AutoSSL        bool
	KeyFile        string
	CertFile       string
	AutoHTTPS      bool
	AutoCORS       bool
	CompatMode     bool
	TargetIsHTTPS  bool
}

type EditHostInput struct {
	ID             int
	IsAdmin        bool
	AllowUserLocal bool
	ClientID       int
	Host           string
	Target         string
	ProxyProtocol  int
	LocalProxy     bool
	Auth           string
	Header         string
	RespHeader     string
	HostChange     string
	Remark         string
	Location       string
	PathRewrite    string
	RedirectURL    string
	FlowLimit      int64
	TimeLimit      string
	ResetFlow      bool
	Scheme         string
	HTTPSJustProxy bool
	TLSOffload     bool
	AutoSSL        bool
	KeyFile        string
	CertFile       string
	AutoHTTPS      bool
	AutoCORS       bool
	CompatMode     bool
	TargetIsHTTPS  bool
}

func (s DefaultIndexService) DashboardData(force bool) map[string]interface{} {
	return s.runtime().DashboardData(force)
}

func (s DefaultIndexService) ListTunnels(input TunnelListInput) ([]*file.Tunnel, int) {
	return s.runtime().ListTunnels(input.Offset, input.Limit, input.Type, input.ClientID, input.Search, input.Sort, input.Order)
}

func (s DefaultIndexService) AddTunnel(input AddTunnelInput) (TunnelMutation, error) {
	id := s.repo().NextTunnelID()
	tunnel := &file.Tunnel{
		Port:       input.Port,
		ServerIp:   input.ServerIP,
		Mode:       input.Mode,
		TargetType: input.TargetType,
		Target: &file.Target{
			TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, ""),
			ProxyProtocol: input.ProxyProtocol,
			LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
		},
		UserAuth: &file.MultiAccount{
			Content:    input.Auth,
			AccountMap: common.DealMultiUser(input.Auth),
		},
		Id:           id,
		Status:       true,
		Remark:       input.Remark,
		Password:     input.Password,
		LocalPath:    input.LocalPath,
		StripPre:     input.StripPre,
		HttpProxy:    input.EnableHTTP,
		Socks5Proxy:  input.EnableSocks5,
		DestAclMode:  normalizeACLMode(input.DestACLMode),
		DestAclRules: normalizeRules(input.DestACLRules),
		Flow: &file.Flow{
			FlowLimit: input.FlowLimit,
			TimeLimit: common.GetTimeNoErrByStr(input.TimeLimit),
		},
	}

	if _, err := s.resolveTunnelPort(tunnel); err != nil {
		return TunnelMutation{}, err
	}

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return TunnelMutation{}, ErrClientNotFound
	}
	tunnel.Client = client
	if tunnel.Client.MaxTunnelNum != 0 && tunnel.Client.GetTunnelNum() >= tunnel.Client.MaxTunnelNum {
		return TunnelMutation{}, ErrTunnelLimitExceeded
	}

	if err := s.repo().CreateTunnel(tunnel); err != nil {
		return TunnelMutation{}, err
	}
	if err := s.runtime().AddTunnel(tunnel); err != nil {
		_ = s.repo().DeleteTunnelRecord(tunnel.Id)
		return TunnelMutation{}, normalizeRuntimeError(err)
	}

	return TunnelMutation{ID: tunnel.Id, ClientID: input.ClientID, Mode: tunnel.Mode}, nil
}

func (s DefaultIndexService) GetTunnel(id int) (*file.Tunnel, error) {
	tunnel, err := s.repo().GetTunnel(id)
	if err != nil {
		return nil, ErrTunnelNotFound
	}
	return tunnel, nil
}

func (s DefaultIndexService) EditTunnel(input EditTunnelInput) (TunnelMutation, error) {
	stored, err := s.repo().GetTunnel(input.ID)
	if err != nil {
		return TunnelMutation{}, ErrTunnelNotFound
	}
	// Edit a copy so that a rejected save leaves the stored task untouched.
	tunnel := stored.Clone()

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return TunnelMutation{}, ErrClientNotFound
	}
	tunnel.Client = client

	desiredMode := input.Mode
	if desiredMode == "" {
		desiredMode = tunnel.Mode
	}
	probe := &file.Tunnel{
		Port:        input.Port,
		Mode:        desiredMode,
		HttpProxy:   input.EnableHTTP,
		Socks5Proxy: input.EnableSocks5,
	}
	if probe.Port <= 0 {
		probe.Port = tunnel.Port
	}
	if probe.Port != tunnel.Port || probe.Mode != tunnel.Mode || probe.Socks5Proxy != tunnel.Socks5Proxy {
		if _, err := s.resolveTunnelPort(probe); err != nil {
			return TunnelMutation{}, err
		}
	}

	targetFallback := ""
	if tunnel.Target != nil {
		targetFallback = tunnel.Target.TargetStr
	}

	tunnel.Port = probe.Port
	tunnel.ServerIp = input.ServerIP
	tunnel.Mode = desiredMode
	tunnel.TargetType = input.TargetType
	tunnel.Target = &file.Target{
		TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, targetFallback),
		ProxyProtocol: input.ProxyProtocol,
		LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
	}
	tunnel.UserAuth = &file.MultiAccount{Content: input.Auth, AccountMap: common.DealMultiUser(input.Auth)}
	tunnel.Password = input.Password
	tunnel.LocalPath = input.LocalPath
	tunnel.StripPre = input.StripPre
	tunnel.HttpProxy = input.EnableHTTP
	tunnel.Socks5Proxy = input.EnableSocks5
	tunnel.DestAclMode = normalizeACLMode(input.DestACLMode)
	tunnel.DestAclRules = normalizeRules(input.DestACLRules)
	tunnel.Remark = input.Remark
	tunnel.Flow.FlowLimit = input.FlowLimit
	tunnel.Flow.TimeLimit = common.GetTimeNoErrByStr(input.TimeLimit)
	if input.ResetFlow {
		tunnel.Flow.ExportFlow = 0
		tunnel.Flow.InletFlow = 0
	}

	if err := s.repo().SaveTunnel(tunnel); err != nil {
		return TunnelMutation{}, err
	}
	if err := s.runtime().StopTunnel(tunnel.Id); err != nil && !isTaskNotRunning(err) {
		return TunnelMutation{}, err
	}
	if err := s.runtime().StartTunnel(tunnel.Id); err != nil {
		return TunnelMutation{}, normalizeRuntimeError(err)
	}

	return TunnelMutation{ID: tunnel.Id, ClientID: input.ClientID, Mode: tunnel.Mode}, nil
}

func (s DefaultIndexService) StopTunnel(id int, mode string) error {
	if mode != "" {
		return changeTunnelStatus(id, mode, "stop")
	}
	if err := s.runtime().StopTunnel(id); err != nil && !isTaskNotRunning(err) {
		return err
	}
	return nil
}

func (s DefaultIndexService) DeleteTunnel(id int) error {
	return s.runtime().DeleteTunnel(id)
}

func (s DefaultIndexService) StartTunnel(id int, mode string) error {
	if mode != "" {
		return changeTunnelStatus(id, mode, "start")
	}
	return normalizeRuntimeError(s.runtime().StartTunnel(id))
}

func (DefaultIndexService) ClearTunnel(id int, mode string) error {
	if strings.TrimSpace(mode) == "" {
		return ErrModeRequired
	}
	return changeTunnelStatus(id, mode, "clear")
}

func (s DefaultIndexService) ListHosts(input HostListInput) ([]*file.Host, int) {
	return s.runtime().ListHosts(input.Offset, input.Limit, input.ClientID, input.Search, input.Sort, input.Order)
}

func (s DefaultIndexService) GetHost(id int) (*file.Host, error) {
	host, err := s.repo().GetHost(id)
	if err != nil {
		return nil, ErrHostNotFound
	}
	return host, nil
}

func (s DefaultIndexService) DeleteHost(id int) error {
	s.runtime().RemoveHostCache(id)
	return s.repo().DeleteHostRecord(id)
}

func (s DefaultIndexService) StartHost(id int, mode string) error {
	s.runtime().RemoveHostCache(id)
	if mode != "" {
		return changeHostStatus(id, mode, "start")
	}
	host, err := s.repo().GetHost(id)
	if err != nil {
		return ErrHostNotFound
	}
	host.IsClose = false
	return s.repo().SaveHost(host, "")
}

func (s DefaultIndexService) StopHost(id int, mode string) error {
	s.runtime().RemoveHostCache(id)
	if mode != "" {
		return changeHostStatus(id, mode, "stop")
	}
	host, err := s.repo().GetHost(id)
	if err != nil {
		return ErrHostNotFound
	}
	host.IsClose = true
	return s.repo().SaveHost(host, "")
}

func (s DefaultIndexService) ClearHost(id int, mode string) error {
	s.runtime().RemoveHostCache(id)
	if strings.TrimSpace(mode) == "" {
		return ErrModeRequired
	}
	return changeHostStatus(id, mode, "clear")
}

func (s DefaultIndexService) AddHost(input AddHostInput) (HostMutation, error) {
	id := s.repo().NextHostID()
	host := &file.Host{
		Id:   id,
		Host: input.Host,
		Target: &file.Target{
			TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, ""),
			ProxyProtocol: input.ProxyProtocol,
			LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
		},
		UserAuth: &file.MultiAccount{
			Content:    input.Auth,
			AccountMap: common.DealMultiUser(input.Auth),
		},
		HeaderChange:     input.Header,
		RespHeaderChange: input.RespHeader,
		HostChange:       input.HostChange,
		Remark:           input.Remark,
		Location:         input.Location,
		PathRewrite:      input.PathRewrite,
		RedirectURL:      input.RedirectURL,
		Flow: &file.Flow{
			FlowLimit: input.FlowLimit,
			TimeLimit: common.GetTimeNoErrByStr(input.TimeLimit),
		},
		Scheme:         normalizeScheme(input.Scheme),
		HttpsJustProxy: input.HTTPSJustProxy,
		TlsOffload:     input.TLSOffload,
		AutoSSL:        input.AutoSSL,
		KeyFile:        input.KeyFile,
		CertFile:       input.CertFile,
		AutoHttps:      input.AutoHTTPS,
		AutoCORS:       input.AutoCORS,
		CompatMode:     input.CompatMode,
		TargetIsHttps:  input.TargetIsHTTPS,
	}

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return HostMutation{}, ErrClientNotFound
	}
	host.Client = client
	if host.Client.MaxTunnelNum != 0 && host.Client.GetTunnelNum() >= host.Client.MaxTunnelNum {
		return HostMutation{}, ErrTunnelLimitExceeded
	}

	if err := s.repo().CreateHost(host); err != nil {
		if isHostExistsError(err) {
			return HostMutation{}, ErrHostExists
		}
		return HostMutation{}, err
	}

	return HostMutation{ID: host.Id, ClientID: input.ClientID}, nil
}

func (s DefaultIndexService) EditHost(input EditHostInput) (HostMutation, error) {
	s.runtime().RemoveHostCache(input.ID)

	host, err := s.repo().GetHost(input.ID)
	if err != nil {
		return HostMutation{}, ErrHostNotFound
	}

	oldHost := host.Host
	scheme := normalizeScheme(input.Scheme)
	if host.Host != input.Host || host.Location != input.Location || host.Scheme != scheme {
		tmpHost := &file.Host{Id: host.Id, Host: input.Host, Location: input.Location, Scheme: scheme}
		if s.repo().HostExists(tmpHost) {
			return HostMutation{}, ErrHostExists
		}
	}

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return HostMutation{}, ErrClientNotFound
	}
	host.Client = client

	targetFallback := ""
	if host.Target != nil {
		targetFallback = host.Target.TargetStr
	}

	host.Host = input.Host
	host.Target = &file.Target{
		TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, targetFallback),
		ProxyProtocol: input.ProxyProtocol,
		LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
	}
	host.UserAuth = &file.MultiAccount{Content: input.Auth, AccountMap: common.DealMultiUser(input.Auth)}
	host.HeaderChange = input.Header
	host.RespHeaderChange = input.RespHeader
	host.HostChange = input.HostChange
	host.Remark = input.Remark
	host.Location = input.Location
	host.PathRewrite = input.PathRewrite
	host.RedirectURL = input.RedirectURL
	host.Scheme = scheme
	host.HttpsJustProxy = input.HTTPSJustProxy
	host.TlsOffload = input.TLSOffload
	host.AutoSSL = input.AutoSSL
	host.KeyFile = input.KeyFile
	host.CertFile = input.CertFile
	host.Flow.FlowLimit = input.FlowLimit
	host.Flow.TimeLimit = common.GetTimeNoErrByStr(input.TimeLimit)
	if input.ResetFlow {
		host.Flow.ExportFlow = 0
		host.Flow.InletFlow = 0
	}
	host.AutoHttps = input.AutoHTTPS
	host.AutoCORS = input.AutoCORS
	host.CompatMode = input.CompatMode
	host.TargetIsHttps = input.TargetIsHTTPS

	host.CertType = common.GetCertType(host.CertFile)
	host.CertHash = crypt.FNV1a64(host.CertType, host.CertFile, host.KeyFile)
	if err := s.repo().SaveHost(host, oldHost); err != nil {
		return HostMutation{}, err
	}

	return HostMutation{ID: host.Id, ClientID: input.ClientID}, nil
}

func sanitizeBridgeTarget(target string, isAdmin bool, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(target, "\r\n", "\n")))
	if !isAdmin && strings.Contains(normalized, "bridge://") {
		return fallback
	}
	return normalized
}

func normalizeACLMode(mode int) int {
	switch mode {
	case file.AclOff, file.AclWhitelist, file.AclBlacklist:
		return mode
	default:
		return file.AclOff
	}
}

func normalizeRules(rules string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(rules, "\r\n", "\n")))
}

func normalizeScheme(scheme string) string {
	switch strings.TrimSpace(strings.ToLower(scheme)) {
	case "http", "https":
		return strings.TrimSpace(strings.ToLower(scheme))
	default:
		return "all"
	}
}

func localProxyEnabled(clientID int, requested bool, allowLocal bool) bool {
	return (clientID > 0 && requested && allowLocal) || clientID <= 0
}

func normalizeRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "the port open error") {
		return ErrPortUnavailable
	}
	return err
}

func isTaskNotRunning(err error) bool {
	return err != nil && strings.Contains(err.Error(), "task is not running")
}

func isHostExistsError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "host has exist")
}

func (s DefaultIndexService) repo() Repository {
	if s.Backend.Repository != nil {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}

func (s DefaultIndexService) runtime() Runtime {
	if s.Backend.Runtime != nil {
		return s.Backend.Runtime
	}
	return DefaultBackend().Runtime
}

func (s DefaultIndexService) resolveTunnelPort(tunnel *file.Tunnel) (int, error) {
	if tunnel.Port <= 0 {
		tunnel.Port = s.runtime().GenerateTunnelPort(tunnel)
	}
	if !s.runtime().TunnelPortAvailable(tunnel) {
		return 0, ErrPortUnavailable
	}
	return tunnel.Port, nil
}
