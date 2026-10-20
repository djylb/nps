package service

import (
	"crypto/subtle"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/skip2/go-qrcode"
)

type ClientService interface {
	List(ListClientsInput) ListClientsResult
	Add(AddClientInput) (int, error)
	Ping(id int, remoteAddr string) (int, error)
	Get(id int) (*file.Client, error)
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
	Rows       []*file.Client
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
	Crypt                   bool
	ConfigConnAllow         bool
	ResetFlow               bool
}

type ClientQRCodeInput struct {
	Text    string
	Account string
	Secret  string
	AppName string
}

func (s DefaultClientService) List(input ListClientsInput) ListClientsResult {
	rows, count := s.repo().ListVisibleClients(input)
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

func (s DefaultClientService) Add(input AddClientInput) (int, error) {
	id := s.repo().NextClientID()
	client := &file.Client{
		VerifyKey: input.VKey,
		Id:        id,
		Status:    true,
		Remark:    input.Remark,
		Cnf: &file.Config{
			U:        input.User,
			P:        input.Password,
			Compress: input.Compress,
			Crypt:    input.Crypt,
		},
		ConfigConnAllow: input.ConfigConnAllow,
		RateLimit:       input.RateLimit,
		MaxConn:         input.MaxConn,
		WebUserName:     input.WebUserName,
		WebPassword:     input.WebPassword,
		WebTotpSecret:   input.WebTotpSecret,
		MaxTunnelNum:    input.MaxTunnelNum,
		Flow: &file.Flow{
			ExportFlow: 0,
			InletFlow:  0,
			FlowLimit:  input.FlowLimit,
			TimeLimit:  common.GetTimeNoErrByStr(input.TimeLimit),
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
	if err := s.repo().CreateClient(client); err != nil {
		return 0, err
	}
	return id, nil
}

func (s DefaultClientService) Ping(id int, remoteAddr string) (int, error) {
	if _, err := s.repo().GetClient(id); err != nil {
		return 0, err
	}
	return s.runtime().PingClient(id, remoteAddr), nil
}

func (s DefaultClientService) Get(id int) (*file.Client, error) {
	return s.repo().GetClient(id)
}

func (s DefaultClientService) Edit(input EditClientInput) error {
	client, err := s.repo().GetClient(input.ID)
	if err != nil {
		return err
	}
	lists, err := input.Access.validate(acl.ScopeUser)
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
		if input.WebUserName == input.ReservedAdminUsername || !s.repo().VerifyUserName(input.WebUserName, client.Id) {
			return ErrWebUsernameDuplicate
		}
	}

	if input.IsAdmin {
		if !s.repo().VerifyVKey(input.VKey, client.Id) {
			return ErrClientVKeyDuplicate
		}
		oldVerifyKey := client.VerifyKey
		client.VerifyKey = input.VKey
		if client.Id > 0 {
			s.repo().ReplaceClientVKeyIndex(oldVerifyKey, client.VerifyKey, client.Id)
		}
		client.Flow.SetLimits(input.FlowLimit, common.GetTimeNoErrByStr(input.TimeLimit))
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
	client.Cnf.Crypt = input.Crypt
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
	return s.repo().SaveClient(client)
}

func (DefaultClientService) Clear(id int, mode string, isAdmin bool) error {
	if !isAdmin || strings.TrimSpace(mode) == "" {
		return ErrClientModifyFailed
	}
	return clearClientStatusByID(id, mode)
}

func (s DefaultClientService) ChangeStatus(id int, status bool) error {
	client, err := s.repo().GetClient(id)
	if err != nil {
		return err
	}
	client.Status = status
	if !client.Status {
		s.runtime().DisconnectClient(client.Id)
	}
	return s.repo().SaveClient(client)
}

// RequestUpdate asks the connected npc of client id to update themselves to
// the latest release and returns how many were asked.
func (s DefaultClientService) RequestUpdate(id int) (int, error) {
	if id <= 0 {
		return 0, ErrVirtualClientOperation
	}
	if _, err := s.repo().GetClient(id); err != nil {
		return 0, ErrClientNotFound
	}
	return s.runtime().RequestClientUpdate(id)
}

func (s DefaultClientService) Delete(id int) error {
	if id <= 0 {
		return ErrVirtualClientOperation
	}
	if err := s.repo().DeleteClient(id); err != nil {
		return err
	}
	s.runtime().DeleteClientResources(id)
	s.runtime().DisconnectClient(id)
	return nil
}

func (s DefaultClientService) RegisterVisitorIP(key, ip string, hours int) (int, time.Time, error) {
	addr, ok := ipreg.ParseIP(strings.TrimSpace(ip))
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
	var found *file.Client
	s.repo().RangeClients(func(c *file.Client) bool {
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
	s.repo().RangeClients(func(c *file.Client) bool {
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
		text = crypt.BuildTotpUri(input.AppName, input.Account, input.Secret)
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

func (s DefaultClientService) repo() Repository {
	if s.Backend.Repository != nil {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}

func (s DefaultClientService) runtime() Runtime {
	if s.Backend.Runtime != nil {
		return s.Backend.Runtime
	}
	return DefaultBackend().Runtime
}
