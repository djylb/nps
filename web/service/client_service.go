package service

import (
	"crypto/subtle"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/textutil"
	"github.com/skip2/go-qrcode"
)

type ClientService interface {
	SaveLaunchForm(id int, form string) error
	Kick(id int) error
	List(ListClientsInput) ListClientsResult
	Add(AddClientInput) (int, error)
	Ping(id int, remoteAddr string) (int, error)
	Get(id int) (*model.Client, error)
	Edit(EditClientInput) error
	Clear(id int, mode string, isAdmin bool) error
	ChangeStatus(id int, status bool) error
	RequestUpdate(id int) (int, error)
	Delete(id int) error
	BuildQRCode(ClientQRCodeInput) ([]byte, error)
	// RegisterVisitorIP registers ip for the client whose registration key
	// or verify key is key, for hours, at most the client's limit.
	RegisterVisitorIP(key, ip string, hours int) (clientID int, until time.Time, err error)
}

type DefaultClientService struct {
	ConfigProvider func() *servercfg.Snapshot
	Backend        Backend
}

type ClientVisibility struct {
	IsAdmin         bool
	PrimaryClientID int
	ClientIDs       []int
}

type ListClientsInput struct {
	Offset     int
	Limit      int
	Search     string
	Sort       string
	Order      string
	Host       string
	Visibility ClientVisibility
}

type ListClientsResult struct {
	Rows       []*model.Client
	Total      int
	BridgeType string
	BridgeAddr string
	BridgeIP   string
	BridgePort int
}

type AddClientInput struct {
	VKey             string
	Remark           string
	User             string
	Password         string
	Compress         bool
	CompressAlg      string
	SchedulerDown    string
	SchedulerUp      string
	Shaping          bool
	Crypt            bool
	ConfigConnAllow  bool
	RateLimit        int
	MaxConn          int
	WebUserName      string
	WebPassword      string
	WebTotpSecret    string
	MaxTunnelNum     int
	FlowLimit        int64
	TimeLimit        string
	Access           AccessListInput
	RegisterKey      string
	RegisterMaxHours int
}

type EditClientInput struct {
	ID                      int
	ReservedAdminUsername   string
	VKey                    string
	Remark                  string
	User                    string
	Password                string
	RateLimit               int
	MaxConn                 int
	WebUserName             string
	WebPassword             string
	WebTotpSecret           string
	MaxTunnelNum            int
	FlowLimit               int64
	TimeLimit               string
	Access                  AccessListInput
	RegisterKey             string
	RegisterMaxHours        int
	IsAdmin                 bool
	AllowUserChangeUsername bool
	Compress                bool
	CompressAlg             string
	SchedulerDown           string
	SchedulerUp             string
	Shaping                 bool
	// Crypt is the new setting of the encryption switch; nil keeps the stored one.
	Crypt           *bool
	ConfigConnAllow bool
	ResetFlow       bool
}

type ClientQRCodeInput struct {
	Text    string
	Account string
	Secret  string
	AppName string
}

func (s DefaultClientService) List(input ListClientsInput) ListClientsResult {
	var rows []*model.Client
	var count int
	if input.Visibility.IsAdmin || len(visibleClientSet(input.Visibility)) == 1 {
		// the running server refreshes what it knows of the clients
		rows, count = s.Backend.Runtime.ListClients(input.Offset, input.Limit, input.Search, input.Sort, input.Order, visibleClientID(input.Visibility))
	} else {
		rows, count = s.Backend.Repository.ListVisibleClients(input)
	}
	bridge := BestBridge(s.config(), input.Host)
	port, _ := strconv.Atoi(bridge.Port)
	return ListClientsResult{
		Rows:       rows,
		Total:      count,
		BridgeType: bridge.Type,
		BridgeAddr: bridge.Addr,
		BridgeIP:   bridge.IP,
		BridgePort: port,
	}
}

// normalizeSchedulerUp checks a client's scheduler_up: "" tells npc nothing,
// otherwise it must be one of connprofile.Schedulers.
func normalizeSchedulerUp(v string) (string, error) {
	if v = strings.ToLower(strings.TrimSpace(v)); v == "" {
		return "", nil
	}
	v, err := connprofile.ParseScheduler(v)
	if err != nil {
		return "", ErrInvalidScheduler
	}
	return v, nil
}

func (s DefaultClientService) Add(input AddClientInput) (int, error) {
	schedulerUp, err := normalizeSchedulerUp(input.SchedulerUp)
	if err != nil {
		return 0, err
	}
	id := s.Backend.Repository.NextClientID()
	client := &model.Client{
		VerifyKey: input.VKey,
		Id:        id,
		Status:    true,
		Remark:    input.Remark,
		Cnf: &model.Config{
			U:             input.User,
			P:             input.Password,
			Compress:      input.Compress,
			CompressAlg:   model.NormalizeClientCompress(input.CompressAlg),
			SchedulerDown: input.SchedulerDown,
			SchedulerUp:   schedulerUp,
			Shaping:       input.Shaping,
			Crypt:         input.Crypt,
		},
		ConfigConnAllow: input.ConfigConnAllow,
		RateLimit:       input.RateLimit,
		MaxConn:         input.MaxConn,
		WebUserName:     input.WebUserName,
		WebPassword:     input.WebPassword,
		WebTotpSecret:   input.WebTotpSecret,
		MaxTunnelNum:    input.MaxTunnelNum,
		Flow: &model.Flow{
			ExportFlow: 0,
			InletFlow:  0,
			FlowLimit:  input.FlowLimit,
			TimeLimit:  textutil.GetTimeNoErrByStr(input.TimeLimit),
		},
		CreateTime: time.Now().Format("2006-01-02 15:04:05"),
	}
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return 0, err
	}
	client.RegisterKey = strings.TrimSpace(input.RegisterKey)
	client.RegisterMaxHours = max(input.RegisterMaxHours, 0)
	if err := s.checkRegisterKey(id, client.RegisterKey, client.VerifyKey); err != nil {
		return 0, err
	}
	lists.applyTo(&client.SourceLists, "client "+strconv.Itoa(id))
	if err := s.Backend.Repository.CreateClient(client); err != nil {
		return 0, err
	}
	return id, nil
}

func (s DefaultClientService) Ping(id int, remoteAddr string) (int, error) {
	if _, err := s.Backend.Repository.GetClient(id); err != nil {
		return 0, err
	}
	return s.Backend.Runtime.PingClient(id, remoteAddr), nil
}

func (s DefaultClientService) Get(id int) (*model.Client, error) {
	return s.Backend.Repository.GetClient(id)
}

func (s DefaultClientService) Edit(input EditClientInput) error {
	client, err := s.Backend.Repository.GetClient(input.ID)
	if err != nil {
		return err
	}
	lists, err := input.Access.validate(acl.ScopeUser)
	if err != nil {
		return err
	}
	schedulerUp, err := normalizeSchedulerUp(input.SchedulerUp)
	if err != nil {
		return err
	}
	registerKey := strings.TrimSpace(input.RegisterKey)
	vkey := client.VerifyKey
	if input.IsAdmin {
		vkey = input.VKey
	}
	if err := s.checkRegisterKey(client.Id, registerKey, vkey); err != nil {
		return err
	}

	if input.WebUserName != "" {
		if input.WebUserName == input.ReservedAdminUsername || !s.Backend.Repository.VerifyUserName(input.WebUserName, client.Id) {
			return ErrWebUsernameDuplicate
		}
	}

	if input.IsAdmin {
		if !s.Backend.Repository.VerifyVKey(input.VKey, client.Id) {
			return ErrClientVKeyDuplicate
		}
		oldVerifyKey := client.VerifyKey
		client.VerifyKey = input.VKey
		if client.Id > 0 {
			s.Backend.Repository.ReplaceClientVKeyIndex(oldVerifyKey, client.VerifyKey, client.Id)
		}
		client.Flow.SetLimits(input.FlowLimit, textutil.GetTimeNoErrByStr(input.TimeLimit))
		client.RateLimit = input.RateLimit
		client.SetMaxConn(input.MaxConn)
		client.MaxTunnelNum = input.MaxTunnelNum
		if input.ResetFlow {
			client.Flow.ResetTraffic()
		}
	}

	client.Remark = input.Remark
	client.Cnf.U = input.User
	client.Cnf.P = input.Password
	client.Cnf.Compress = input.Compress
	client.Cnf.CompressAlg = model.NormalizeClientCompress(input.CompressAlg)
	client.Cnf.SchedulerDown = input.SchedulerDown
	client.Cnf.SchedulerUp = schedulerUp
	client.Cnf.Shaping = input.Shaping
	if input.Crypt != nil {
		client.Cnf.Crypt = *input.Crypt
	}
	if input.IsAdmin || input.AllowUserChangeUsername {
		client.WebUserName = input.WebUserName
	}
	client.WebPassword = input.WebPassword
	client.WebTotpSecret = input.WebTotpSecret
	client.EnsureWebPassword()
	client.ConfigConnAllow = input.ConfigConnAllow

	var limit int64
	if client.RateLimit > 0 {
		limit = int64(client.RateLimit) * 1024
	}
	if client.Rate == nil {
		client.Rate = rate.NewRate(limit)
		client.Rate.Start()
	} else if client.Rate.Limit() != limit {
		client.Rate.ResetLimit(limit)
	} else {
		client.Rate.Start()
	}

	client.RegisterKey = registerKey
	client.RegisterMaxHours = max(input.RegisterMaxHours, 0)
	lists.applyTo(&client.SourceLists, "client "+strconv.Itoa(client.Id))
	return s.Backend.Repository.SaveClient(client)
}

// SaveLaunchForm stores the start command form of client id; empty removes it.
func (s DefaultClientService) SaveLaunchForm(id int, form string) error {
	if len(form) > MaxLaunchForm {
		return ErrLaunchFormTooLarge
	}
	client, err := s.Backend.Repository.GetClient(id)
	if err != nil {
		return err
	}
	client.LaunchForm = form
	return s.Backend.Repository.SaveClient(client)
}

func (s DefaultClientService) Clear(id int, mode string, isAdmin bool) error {
	if !isAdmin || strings.TrimSpace(mode) == "" {
		return ErrClientModifyFailed
	}
	return clearClientStatusByID(s.Backend.Repository, id, mode)
}

func (s DefaultClientService) ChangeStatus(id int, status bool) error {
	client, err := s.Backend.Repository.GetClient(id)
	if err != nil {
		return err
	}
	client.Status = status
	if !client.Status {
		s.Backend.Runtime.DisconnectClient(client.Id)
	}
	return s.Backend.Repository.SaveClient(client)
}

// Kick ends the sessions of client id. Its configuration stays, so an npc
// that reconnects by itself is back after its retry delay; to keep it out, use
// ChangeStatus.
func (s DefaultClientService) Kick(id int) error {
	if id <= 0 {
		return ErrVirtualClientOperation
	}
	if _, err := s.Backend.Repository.GetClient(id); err != nil {
		return ErrClientNotFound
	}
	if !s.Backend.Runtime.ClientConnected(id) {
		return ErrClientOffline
	}
	s.Backend.Runtime.DisconnectClient(id)
	return nil
}

// RequestUpdate asks the connected npc of client id to update themselves to
// the latest release and returns how many were asked.
func (s DefaultClientService) RequestUpdate(id int) (int, error) {
	if id <= 0 {
		return 0, ErrVirtualClientOperation
	}
	if _, err := s.Backend.Repository.GetClient(id); err != nil {
		return 0, ErrClientNotFound
	}
	return s.Backend.Runtime.RequestClientUpdate(id)
}

func (s DefaultClientService) Delete(id int) error {
	if id <= 0 {
		return ErrVirtualClientOperation
	}
	if err := s.Backend.Repository.DeleteClient(id); err != nil {
		return err
	}
	s.Backend.Runtime.DeleteClientResources(id)
	s.Backend.Runtime.DisconnectClient(id)
	return nil
}

func (s DefaultClientService) RegisterVisitorIP(key, ip string, hours int) (int, time.Time, error) {
	addr, ok := netaddr.ParseIP(strings.TrimSpace(ip))
	if !ok {
		return 0, time.Time{}, ErrRegisterKeyInvalid
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, time.Time{}, ErrRegisterKeyInvalid
	}
	cfg := s.config()
	def := max(cfg.App.IPRegisterMaxHours, 1)
	// the public key is an administrator's key, as an administrator's login
	// it registers for every client
	if keyEqual(cfg.Runtime.PublicVKey, key) {
		until := ipreg.Register(ipreg.AnyClient, addr, time.Duration(clampHours(hours, def))*time.Hour)
		return ipreg.AnyClient, until, nil
	}
	var found *model.Client
	s.Backend.Repository.RangeClients(func(c *model.Client) bool {
		if c.Id > 0 && c.Status && !c.NoDisplay && (keyEqual(c.RegisterKey, key) || keyEqual(c.VerifyKey, key)) {
			found = c
			return false
		}
		return true
	})
	if found == nil {
		return 0, time.Time{}, ErrRegisterKeyInvalid
	}
	until := ipreg.Register(found.Id, addr, time.Duration(clampHours(hours, found.RegisterHoursLimit(def)))*time.Hour)
	return found.Id, until, nil
}

// clampHours returns hours, or limit when hours is unset or above it.
func clampHours(hours, limit int) int {
	if hours <= 0 || hours > limit {
		return limit
	}
	return hours
}

// keyEqual compares a stored key with a submitted one in constant time; an
// unset key matches nothing.
func keyEqual(stored, submitted string) bool {
	return stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(submitted)) == 1
}

// checkRegisterKey rejects a registration key that another client uses as
// registration key or verify key, or that is the client's own verify key.
func (s DefaultClientService) checkRegisterKey(id int, key, vkey string) error {
	if key == "" {
		return nil
	}
	if key == vkey {
		return ErrRegisterKeyDuplicate
	}
	var dup bool
	s.Backend.Repository.RangeClients(func(c *model.Client) bool {
		if c.Id != id && (c.RegisterKey == key || c.VerifyKey == key) {
			dup = true
			return false
		}
		return true
	})
	if dup {
		return ErrRegisterKeyDuplicate
	}
	return nil
}

func (DefaultClientService) BuildQRCode(input ClientQRCodeInput) ([]byte, error) {
	text := input.Text
	if text != "" {
		if decoded, err := url.QueryUnescape(text); err == nil {
			text = decoded
		}
	} else if input.Account != "" && input.Secret != "" {
		uri, err := crypt.BuildTotpUri(input.AppName, input.Account, input.Secret)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrClientQRCodeSecretInvalid, err)
		}
		text = uri
	}
	if text == "" {
		return nil, ErrClientQRCodeTextRequired
	}
	return qrcode.Encode(text, qrcode.Medium, 256)
}

func visibleClientSet(visibility ClientVisibility) map[int]struct{} {
	if visibility.IsAdmin {
		return nil
	}
	visible := make(map[int]struct{})
	if visibility.PrimaryClientID > 0 {
		visible[visibility.PrimaryClientID] = struct{}{}
	}
	for _, clientID := range visibility.ClientIDs {
		if clientID > 0 {
			visible[clientID] = struct{}{}
		}
	}
	return visible
}

func visibleClientID(visibility ClientVisibility) int {
	if visibility.PrimaryClientID > 0 {
		return visibility.PrimaryClientID
	}
	for _, clientID := range visibility.ClientIDs {
		if clientID > 0 {
			return clientID
		}
	}
	return 0
}

func (s DefaultClientService) config() *servercfg.Snapshot {
	if s.ConfigProvider != nil {
		if cfg := s.ConfigProvider(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}
