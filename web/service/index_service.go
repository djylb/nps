package service

import (
	"html"
	"strconv"
	"strings"
	"time"

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
	AddTunnel(TunnelInput) (TunnelMutation, error)
	GetTunnel(id int) (*model.Tunnel, error)
	EditTunnel(TunnelInput) (TunnelMutation, error)
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
	AddHost(HostInput) (HostMutation, error)
	EditHost(HostInput) (HostMutation, error)
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

// TunnelInput is a submitted tunnel. ID and ResetFlow are only read when an
// existing tunnel is edited.
type TunnelInput struct {
	ID             int
	ClientID       int
	IsAdmin        bool
	AllowUserLocal bool
	ResetFlow      bool
	Access         AccessListInput
	// Tunnel holds the values of model.TunnelSettings as submitted.
	Tunnel *model.Tunnel
}

// HostInput is a submitted host, as TunnelInput is a tunnel.
type HostInput struct {
	ID             int
	ClientID       int
	IsAdmin        bool
	AllowUserLocal bool
	ResetFlow      bool
	Access         AccessListInput
	// Host holds the values of model.HostSettings as submitted.
	Host *model.Host
}

func (in TunnelInput) form() *model.Tunnel {
	if in.Tunnel == nil {
		return new(model.Tunnel)
	}
	return in.Tunnel
}

func (in HostInput) form() *model.Host {
	if in.Host == nil {
		return new(model.Host)
	}
	return in.Host
}

func (s DefaultIndexService) DashboardData(force bool) map[string]interface{} {
	return s.runtime().DashboardData(force)
}

func (s DefaultIndexService) ListTunnels(input TunnelListInput) ([]*model.Tunnel, int) {
	return s.runtime().ListTunnels(input.Offset, input.Limit, input.Type, input.ClientID, input.Search, input.Sort, input.Order)
}

func (s DefaultIndexService) AddTunnel(input TunnelInput) (TunnelMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return TunnelMutation{}, err
	}
	form := input.form()
	destRules, err := normalizeDestRules(form.DestAclRules)
	if err != nil {
		return TunnelMutation{}, err
	}
	id := s.repo().NextTunnelID()
	flowLimit, timeLimit := formFlowLimits(form.Flow)
	tunnel := &model.Tunnel{
		Port:   form.Port,
		Mode:   form.Mode,
		Id:     id,
		Status: true,
		Flow:   &model.Flow{FlowLimit: flowLimit, TimeLimit: timeLimit},
	}
	applyTunnelForm(tunnel, form, input, input.ClientID, "", destRules)
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

func (s DefaultIndexService) EditTunnel(input TunnelInput) (TunnelMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return TunnelMutation{}, err
	}
	form := input.form()
	destRules, err := normalizeDestRules(form.DestAclRules)
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

	desiredMode := form.Mode
	if desiredMode == "" {
		desiredMode = tunnel.Mode
	}
	probe := &model.Tunnel{
		Port:        form.Port,
		Mode:        desiredMode,
		HttpProxy:   form.HttpProxy,
		Socks5Proxy: form.Socks5Proxy,
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
	tunnel.Mode = desiredMode
	applyTunnelForm(tunnel, form, input, client.Id, targetFallback, destRules)
	lists.applyTo(&tunnel.SourceLists, "tunnel "+strconv.Itoa(tunnel.Id))
	tunnel.Flow.SetLimits(formFlowLimits(form.Flow))

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

// applyTunnelForm sets on tunnel the settings of the submitted form other
// than its port, mode and flow limits, which adding and editing decide
// differently. clientID is the owner the tunnel gets, fallback the target
// kept for a target the user may not set and destRules the checked
// destination list.
func applyTunnelForm(tunnel, form *model.Tunnel, input TunnelInput, clientID int, fallback, destRules string) {
	model.CopyFormSettings(model.TunnelSettings, tunnel, form)
	tunnel.Target = formTarget(form.Target, input.IsAdmin, input.AllowUserLocal, clientID, fallback)
	tunnel.UserAuth = formAccounts(form.UserAuth)
	tunnel.DestAclMode = normalizeACLMode(form.DestAclMode)
	tunnel.DestAclRules = destRules
	tunnel.SetHealthCheck(formHealthCheck(form.HealthConfig))
	tunnel.SetMaxConn(form.MaxConn)
	tunnel.SetRateLimits(form.UploadLimit, form.DownloadLimit)
}

// applyHostForm sets on host the settings of the submitted form other than
// its target and flow limits, which adding and editing set differently.
func applyHostForm(host, form *model.Host) {
	model.CopyFormSettings(model.HostSettings, host, form)
	host.Scheme = normalizeScheme(form.Scheme)
	host.UserAuth = formAccounts(form.UserAuth)
	host.SetHealthCheck(formHealthCheck(form.HealthConfig))
	host.SetMaxConn(form.MaxConn)
	host.SetRateLimits(form.UploadLimit, form.DownloadLimit)
}

// formTarget is the target of a submitted entry of the client clientID.
func formTarget(t *model.Target, isAdmin, allowLocal bool, clientID int, fallback string) *model.Target {
	if t == nil {
		t = new(model.Target)
	}
	return &model.Target{
		TargetStr:     sanitizeBridgeTarget(t.TargetStr, isAdmin, fallback),
		ProxyProtocol: t.ProxyProtocol,
		LocalProxy:    localProxyEnabled(clientID, t.LocalProxy, allowLocal),
	}
}

func formAccounts(a *model.MultiAccount) *model.MultiAccount {
	var content string
	if a != nil {
		content = a.Content
	}
	return &model.MultiAccount{Content: content, AccountMap: auth.DealMultiUser(content)}
}

func formHealthCheck(h *model.HealthCheck) model.HealthCheck {
	if h == nil {
		return model.HealthCheck{}
	}
	return *h
}

func formFlowLimits(f *model.Flow) (int64, time.Time) {
	if f == nil {
		return 0, time.Time{}
	}
	return f.FlowLimit, f.TimeLimit
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

func (s DefaultIndexService) AddHost(input HostInput) (HostMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return HostMutation{}, err
	}
	form := input.form()
	id := s.repo().NextHostID()
	flowLimit, timeLimit := formFlowLimits(form.Flow)
	host := &model.Host{
		Id:     id,
		Target: formTarget(form.Target, input.IsAdmin, input.AllowUserLocal, input.ClientID, ""),
		Flow:   &model.Flow{FlowLimit: flowLimit, TimeLimit: timeLimit},
	}
	applyHostForm(host, form)
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

func (s DefaultIndexService) EditHost(input HostInput) (HostMutation, error) {
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return HostMutation{}, err
	}
	form := input.form()
	// After the edit: a request in between would cache a transport of the old settings.
	defer s.runtime().RemoveHostCache(input.ID)

	host, err := s.repo().GetHost(input.ID)
	if err != nil {
		return HostMutation{}, ErrHostNotFound
	}

	oldHost := host.Host
	scheme := normalizeScheme(form.Scheme)
	if host.Host != form.Host || host.Location != form.Location || host.Scheme != scheme {
		tmpHost := &model.Host{Id: host.Id, Host: form.Host, Location: form.Location, Scheme: scheme}
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

	target := formTarget(form.Target, input.IsAdmin, input.AllowUserLocal, client.Id, targetFallback)
	host.Lock()
	host.Client, host.Target = client, target
	host.Unlock()
	applyHostForm(host, form)
	host.Flow.SetLimits(formFlowLimits(form.Flow))
	if input.ResetFlow {
		host.Flow.ResetTraffic()
	}

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
