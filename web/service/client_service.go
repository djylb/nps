package service

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
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
	Delete(id int) error
	BuildQRCode(ClientQRCodeInput) ([]byte, error)
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
	VKey            string
	Remark          string
	User            string
	Password        string
	Compress        bool
	Crypt           bool
	ConfigConnAllow bool
	RateLimit       int
	MaxConn         int
	WebUserName     string
	WebPassword     string
	WebTotpSecret   string
	MaxTunnelNum    int
	FlowLimit       int64
	TimeLimit       string
	BlackIPList     []string
}

type EditClientInput struct {
	ID                      int
	IsAdmin                 bool
	AllowUserChangeUsername bool
	ReservedAdminUsername   string
	VKey                    string
	Remark                  string
	User                    string
	Password                string
	Compress                bool
	Crypt                   bool
	ConfigConnAllow         bool
	RateLimit               int
	MaxConn                 int
	WebUserName             string
	WebPassword             string
	WebTotpSecret           string
	MaxTunnelNum            int
	FlowLimit               int64
	TimeLimit               string
	ResetFlow               bool
	BlackIPList             []string
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
		BlackIpList: input.BlackIPList,
		CreateTime:  time.Now().Format("2006-01-02 15:04:05"),
	}
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
		s.repo().ReplaceClientVKeyIndex(oldVerifyKey, client.VerifyKey, client.Id)
		client.Flow.FlowLimit = input.FlowLimit
		client.Flow.TimeLimit = common.GetTimeNoErrByStr(input.TimeLimit)
		client.RateLimit = input.RateLimit
		client.MaxConn = input.MaxConn
		client.MaxTunnelNum = input.MaxTunnelNum
		if input.ResetFlow {
			client.Flow.ExportFlow = 0
			client.Flow.InletFlow = 0
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

	client.BlackIpList = input.BlackIPList
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

func (s DefaultClientService) Delete(id int) error {
	if err := s.repo().DeleteClient(id); err != nil {
		return err
	}
	s.runtime().DeleteClientResources(id)
	s.runtime().DisconnectClient(id)
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
