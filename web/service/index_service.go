package service

import (
	"html"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/auth"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/textutil"
)

type IndexService interface {
	DashboardData(force bool) map[string]interface{}
	SuggestCert(host string, excludeID, clientID int) *CertSuggestion
	ListTunnels(TunnelListInput) ([]*model.Tunnel, int)
	AddTunnel(AddTunnelInput) (TunnelMutation, error)
	GetTunnel(id int) (*model.Tunnel, error)
	EditTunnel(EditTunnelInput) (TunnelMutation, error)
	StopTunnel(id int, mode string) error
	DeleteTunnel(id int) error
	StartTunnel(id int, mode string) error
	ClearTunnel(id int, mode string) error
	ListHosts(HostListInput) ([]*model.Host, int)
	GetHost(id int) (*model.Host, error)
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
	ClientID       int
	Port           int
	ServerIP       string
	Mode           string
	TargetType     string
	Target         string
	ProxyProtocol  int
	HealthCheck    model.HealthCheck
	Auth           string
	Remark         string
	Password       string
	LocalPath      string
	StripPre       string
	DestACLMode    int
	DestACLRules   string
	Access         AccessListInput
	MaxConn        int
	UploadLimit    int // KB/s; zero means unlimited
	DownloadLimit  int // KB/s; zero means unlimited
	FlowLimit      int64
	TimeLimit      string
	IsAdmin        bool
	AllowUserLocal bool
	LocalProxy     bool
	EnableHTTP     bool
	EnableSocks5   bool
}

type EditTunnelInput struct {
	ID             int
	ClientID       int
	Port           int
	ServerIP       string
	Mode           string
	TargetType     string
	Target         string
	ProxyProtocol  int
	HealthCheck    model.HealthCheck
	Auth           string
	Remark         string
	Password       string
	LocalPath      string
	StripPre       string
	DestACLMode    int
	DestACLRules   string
	Access         AccessListInput
	MaxConn        int
	UploadLimit    int // KB/s; zero means unlimited
	DownloadLimit  int // KB/s; zero means unlimited
	FlowLimit      int64
	TimeLimit      string
	IsAdmin        bool
	AllowUserLocal bool
	LocalProxy     bool
	EnableHTTP     bool
	EnableSocks5   bool
	ResetFlow      bool
}

type AddHostInput struct {
	ClientID       int
	Host           string
	Target         string
	ProxyProtocol  int
	HealthCheck    model.HealthCheck
	Auth           string
	Header         string
	RespHeader     string
	HostChange     string
	Remark         string
	Location       string
	PathRewrite    string
	RedirectURL    string
	Access         AccessListInput
	MaxConn        int
	UploadLimit    int // KB/s; zero means unlimited
	DownloadLimit  int // KB/s; zero means unlimited
	FlowLimit      int64
	TimeLimit      string
	Scheme         string
	KeyFile        string
	CertFile       string
	IsAdmin        bool
	AllowUserLocal bool
	LocalProxy     bool
	HTTPSJustProxy bool
	TLSOffload     bool
	AutoSSL        bool
	AutoHTTPS      bool
	AutoCORS       bool
	CompatMode     bool
	TargetIsHTTPS  bool
}

type EditHostInput struct {
	ID             int
	ClientID       int
	Host           string
	Target         string
	ProxyProtocol  int
	HealthCheck    model.HealthCheck
	Auth           string
	Header         string
	RespHeader     string
	HostChange     string
	Remark         string
	Location       string
	PathRewrite    string
	RedirectURL    string
	Access         AccessListInput
	MaxConn        int
	UploadLimit    int // KB/s; zero means unlimited
	DownloadLimit  int // KB/s; zero means unlimited
	FlowLimit      int64
	TimeLimit      string
	Scheme         string
	KeyFile        string
	CertFile       string
	IsAdmin        bool
	AllowUserLocal bool
	LocalProxy     bool
	ResetFlow      bool
	HTTPSJustProxy bool
	TLSOffload     bool
	AutoSSL        bool
	AutoHTTPS      bool
	AutoCORS       bool
	CompatMode     bool
	TargetIsHTTPS  bool
}

func (s DefaultIndexService) DashboardData(force bool) map[string]interface{} {
	return s.runtime().DashboardData(force)
}

func (s DefaultIndexService) ListTunnels(input TunnelListInput) ([]*model.Tunnel, int) {
	return s.runtime().ListTunnels(input.Offset, input.Limit, input.Type, input.ClientID, input.Search, input.Sort, input.Order)
}

func (s DefaultIndexService) AddTunnel(input AddTunnelInput) (TunnelMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return TunnelMutation{}, err
	}
	destRules, err := normalizeDestRules(input.DestACLRules)
	if err != nil {
		return TunnelMutation{}, err
	}
	id := s.repo().NextTunnelID()
	tunnel := &model.Tunnel{
		Port:       input.Port,
		ServerIp:   input.ServerIP,
		Mode:       input.Mode,
		TargetType: input.TargetType,
		Target: &model.Target{
			TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, ""),
			ProxyProtocol: input.ProxyProtocol,
			LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
		},
		UserAuth: &model.MultiAccount{
			Content:    input.Auth,
			AccountMap: auth.DealMultiUser(input.Auth),
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
		DestAclRules: destRules,
		Flow: &model.Flow{
			FlowLimit: input.FlowLimit,
			TimeLimit: textutil.GetTimeNoErrByStr(input.TimeLimit),
		},
	}

	tunnel.SetHealthCheck(input.HealthCheck)
	tunnel.SetMaxConn(input.MaxConn)
	tunnel.SetRateLimits(input.UploadLimit, input.DownloadLimit)
	lists.applyTo(&tunnel.SourceLists, "tunnel "+strconv.Itoa(id))

	if _, err := s.resolveTunnelPort(tunnel); err != nil {
		return TunnelMutation{}, err
	}

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return TunnelMutation{}, ErrClientNotFound
	}
	tunnel.Client = client
	if tunnel.Client.MaxTunnelNum != 0 && file.GetDb().ClientEntryCount(tunnel.Client) >= tunnel.Client.MaxTunnelNum {
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

func (s DefaultIndexService) GetTunnel(id int) (*model.Tunnel, error) {
	tunnel, err := s.repo().GetTunnel(id)
	if err != nil {
		return nil, ErrTunnelNotFound
	}
	return tunnel, nil
}

func (s DefaultIndexService) EditTunnel(input EditTunnelInput) (TunnelMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return TunnelMutation{}, err
	}
	destRules, err := normalizeDestRules(input.DestACLRules)
	if err != nil {
		return TunnelMutation{}, err
	}
	stored, err := s.repo().GetTunnel(input.ID)
	if err != nil {
		return TunnelMutation{}, ErrTunnelNotFound
	}
	// Edit a copy so that a rejected save leaves the stored task untouched.
	tunnel := stored.Clone()

	client, err := s.editTargetClient(tunnel.Client, input.ClientID)
	if err != nil {
		return TunnelMutation{}, err
	}
	tunnel.Client = client

	desiredMode := input.Mode
	if desiredMode == "" {
		desiredMode = tunnel.Mode
	}
	probe := &model.Tunnel{
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
	tunnel.Target = &model.Target{
		TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, targetFallback),
		ProxyProtocol: input.ProxyProtocol,
		LocalProxy:    localProxyEnabled(client.Id, input.LocalProxy, input.AllowUserLocal),
	}
	tunnel.UserAuth = &model.MultiAccount{Content: input.Auth, AccountMap: auth.DealMultiUser(input.Auth)}
	tunnel.Password = input.Password
	tunnel.LocalPath = input.LocalPath
	tunnel.StripPre = input.StripPre
	tunnel.HttpProxy = input.EnableHTTP
	tunnel.Socks5Proxy = input.EnableSocks5
	tunnel.DestAclMode = normalizeACLMode(input.DestACLMode)
	tunnel.DestAclRules = destRules
	lists.applyTo(&tunnel.SourceLists, "tunnel "+strconv.Itoa(tunnel.Id))
	tunnel.Remark = input.Remark
	tunnel.SetHealthCheck(input.HealthCheck)
	tunnel.SetMaxConn(input.MaxConn)
	tunnel.SetRateLimits(input.UploadLimit, input.DownloadLimit)
	tunnel.Flow.SetLimits(input.FlowLimit, textutil.GetTimeNoErrByStr(input.TimeLimit))

	if err := s.repo().SaveTunnel(tunnel); err != nil {
		return TunnelMutation{}, err
	}
	if input.ResetFlow {
		tunnel.Flow.ResetTraffic()
	}
	if err := s.runtime().StopTunnel(tunnel.Id); err != nil && !isTaskNotRunning(err) {
		return TunnelMutation{}, err
	}
	if err := s.runtime().StartTunnel(tunnel.Id); err != nil {
		return TunnelMutation{}, normalizeRuntimeError(err)
	}

	return TunnelMutation{ID: tunnel.Id, ClientID: client.Id, Mode: tunnel.Mode}, nil
}

// editTargetClient returns the client an edited tunnel or host belongs to.
// A zero clientID keeps the current client; moving to another client
// applies the same tunnel count limit as creation.
func (s DefaultIndexService) editTargetClient(current *model.Client, clientID int) (*model.Client, error) {
	if clientID == 0 {
		if current == nil {
			return nil, ErrClientNotFound
		}
		return current, nil
	}
	client, err := s.repo().GetClient(clientID)
	if err != nil {
		return nil, ErrClientNotFound
	}
	if current != nil && current.Id == client.Id {
		return client, nil
	}
	if client.MaxTunnelNum != 0 && file.GetDb().ClientEntryCount(client) >= client.MaxTunnelNum {
		return nil, ErrTunnelLimitExceeded
	}
	return client, nil
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

func (s DefaultIndexService) ClearTunnel(id int, mode string) error {
	if strings.TrimSpace(mode) == "max_conn" {
		tunnel, err := s.GetTunnel(id)
		if err != nil {
			return err
		}
		tunnel.SetMaxConn(0)
		return s.repo().SaveTunnel(tunnel)
	}
	if mode = strings.TrimSpace(mode); mode == "upload_limit" || mode == "download_limit" {
		tunnel, err := s.GetTunnel(id)
		if err != nil {
			return err
		}
		if mode == "upload_limit" {
			tunnel.ClearUploadLimit()
		} else {
			tunnel.ClearDownloadLimit()
		}
		return s.repo().SaveTunnel(tunnel)
	}
	if strings.TrimSpace(mode) == "" {
		return ErrModeRequired
	}
	return changeTunnelStatus(id, mode, "clear")
}

func (s DefaultIndexService) ListHosts(input HostListInput) ([]*model.Host, int) {
	return s.runtime().ListHosts(input.Offset, input.Limit, input.ClientID, input.Search, input.Sort, input.Order)
}

func (s DefaultIndexService) GetHost(id int) (*model.Host, error) {
	host, err := s.repo().GetHost(id)
	if err != nil {
		return nil, ErrHostNotFound
	}
	return host, nil
}

// CertSuggestion is an existing certificate a domain entry could use. The
// files are in the form the certificate fields of the form take.
type CertSuggestion struct {
	SourceID       int    `json:"source_id"`
	SourceHost     string `json:"source_host"`
	SourceLocation string `json:"source_location"`
	CertFile       string `json:"cert_file"`
	KeyFile        string `json:"key_file"`
}

// SuggestCert returns the certificate FindCertByHost's choice would give the
// name host, among entries with certificate files other than excludeID; with a
// clientID other than 0, among the entries of that client. Nil means none.
func (s DefaultIndexService) SuggestCert(host string, excludeID, clientID int) *CertSuggestion {
	source, err := s.repo().FindReusableCert(strings.TrimSpace(host), excludeID, clientID)
	if err != nil || source == nil {
		return nil
	}
	return &CertSuggestion{
		SourceID:       source.Id,
		SourceHost:     source.Host,
		SourceLocation: source.Location,
		CertFile:       html.UnescapeString(source.CertFile), // stored escaped by the form handler
		KeyFile:        html.UnescapeString(source.KeyFile),
	}
}

func (s DefaultIndexService) DeleteHost(id int) error {
	defer s.runtime().RemoveHostCache(id)
	return s.repo().DeleteHostRecord(id)
}

func (s DefaultIndexService) StartHost(id int, mode string) error {
	defer s.runtime().RemoveHostCache(id)
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
	defer s.runtime().RemoveHostCache(id)
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
	if strings.TrimSpace(mode) == "max_conn" {
		host, err := s.GetHost(id)
		if err != nil {
			return err
		}
		host.SetMaxConn(0)
		return s.repo().SaveHost(host, "")
	}
	if mode = strings.TrimSpace(mode); mode == "upload_limit" || mode == "download_limit" {
		host, err := s.GetHost(id)
		if err != nil {
			return err
		}
		if mode == "upload_limit" {
			host.ClearUploadLimit()
		} else {
			host.ClearDownloadLimit()
		}
		return s.repo().SaveHost(host, "")
	}
	defer s.runtime().RemoveHostCache(id)
	if strings.TrimSpace(mode) == "" {
		return ErrModeRequired
	}
	return changeHostStatus(id, mode, "clear")
}

func (s DefaultIndexService) AddHost(input AddHostInput) (HostMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return HostMutation{}, err
	}
	id := s.repo().NextHostID()
	host := &model.Host{
		Id:   id,
		Host: input.Host,
		Target: &model.Target{
			TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, ""),
			ProxyProtocol: input.ProxyProtocol,
			LocalProxy:    localProxyEnabled(input.ClientID, input.LocalProxy, input.AllowUserLocal),
		},
		UserAuth: &model.MultiAccount{
			Content:    input.Auth,
			AccountMap: auth.DealMultiUser(input.Auth),
		},
		HeaderChange:     input.Header,
		RespHeaderChange: input.RespHeader,
		HostChange:       input.HostChange,
		Remark:           input.Remark,
		Location:         input.Location,
		PathRewrite:      input.PathRewrite,
		RedirectURL:      input.RedirectURL,
		Flow: &model.Flow{
			FlowLimit: input.FlowLimit,
			TimeLimit: textutil.GetTimeNoErrByStr(input.TimeLimit),
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
	host.SetHealthCheck(input.HealthCheck)
	host.SetMaxConn(input.MaxConn)
	host.SetRateLimits(input.UploadLimit, input.DownloadLimit)
	lists.applyTo(&host.SourceLists, "host "+strconv.Itoa(id)+" ("+host.Host+")")

	client, err := s.repo().GetClient(input.ClientID)
	if err != nil {
		return HostMutation{}, ErrClientNotFound
	}
	host.Client = client
	if host.Client.MaxTunnelNum != 0 && file.GetDb().ClientEntryCount(host.Client) >= host.Client.MaxTunnelNum {
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
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return HostMutation{}, err
	}
	// After the edit: a request in between would cache a transport of the old settings.
	defer s.runtime().RemoveHostCache(input.ID)

	host, err := s.repo().GetHost(input.ID)
	if err != nil {
		return HostMutation{}, ErrHostNotFound
	}

	oldHost := host.Host
	scheme := normalizeScheme(input.Scheme)
	if host.Host != input.Host || host.Location != input.Location || host.Scheme != scheme {
		tmpHost := &model.Host{Id: host.Id, Host: input.Host, Location: input.Location, Scheme: scheme}
		if s.repo().HostExists(tmpHost) {
			return HostMutation{}, ErrHostExists
		}
	}

	client, err := s.editTargetClient(host.Client, input.ClientID)
	if err != nil {
		return HostMutation{}, err
	}
	targetFallback := ""
	if host.Target != nil {
		targetFallback = host.Target.TargetStr
	}

	host.Host = input.Host
	target := &model.Target{
		TargetStr:     sanitizeBridgeTarget(input.Target, input.IsAdmin, targetFallback),
		ProxyProtocol: input.ProxyProtocol,
		LocalProxy:    localProxyEnabled(client.Id, input.LocalProxy, input.AllowUserLocal),
	}
	host.Lock()
	host.Client, host.Target = client, target
	host.Unlock()
	host.SetHealthCheck(input.HealthCheck)
	host.UserAuth = &model.MultiAccount{Content: input.Auth, AccountMap: auth.DealMultiUser(input.Auth)}
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
	host.SetMaxConn(input.MaxConn)
	host.SetRateLimits(input.UploadLimit, input.DownloadLimit)
	host.Flow.SetLimits(input.FlowLimit, textutil.GetTimeNoErrByStr(input.TimeLimit))
	if input.ResetFlow {
		host.Flow.ResetTraffic()
	}
	host.AutoHttps = input.AutoHTTPS
	host.AutoCORS = input.AutoCORS
	host.CompatMode = input.CompatMode
	host.TargetIsHttps = input.TargetIsHTTPS

	host.CertType = crypt.GetCertType(host.CertFile)
	host.CertHash = crypt.FNV1a64(host.CertType, host.CertFile, host.KeyFile)
	lists.applyTo(&host.SourceLists, "host "+strconv.Itoa(host.Id)+" ("+host.Host+")")
	if err := s.repo().SaveHost(host, oldHost); err != nil {
		return HostMutation{}, err
	}

	host.ControlGroup() // Move live slots together with their new owner.

	return HostMutation{ID: host.Id, ClientID: client.Id}, nil
}

func sanitizeBridgeTarget(target string, isAdmin bool, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(textutil.NormalizeNewlines(target)))
	if !isAdmin && strings.Contains(normalized, "bridge://") {
		return fallback
	}
	return normalized
}

func normalizeACLMode(mode int) int {
	switch mode {
	case model.AclOff, model.AclWhitelist, model.AclBlacklist:
		return mode
	default:
		return model.AclOff
	}
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

func (s DefaultIndexService) resolveTunnelPort(tunnel *model.Tunnel) (int, error) {
	if tunnel.Port <= 0 {
		tunnel.Port = s.runtime().GenerateTunnelPort(tunnel)
	}
	if !s.runtime().TunnelPortAvailable(tunnel) {
		return 0, ErrPortUnavailable
	}
	return tunnel.Port, nil
}
