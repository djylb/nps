package file

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

type Flow struct {
	ExportFlow int64     // 传出流量
	InletFlow  int64     // 传入流量
	FlowLimit  int64     // 流量限制
	TimeLimit  time.Time // 连接到期时间
	sync.RWMutex
}

func (s *Flow) Add(in, out int64) {
	s.Lock()
	s.InletFlow += in
	s.ExportFlow += out
	s.Unlock()
}

func (s *Flow) Sub(in, out int64) {
	s.Lock()
	s.InletFlow -= in
	s.ExportFlow -= out
	if s.InletFlow < 0 {
		s.InletFlow = 0
	}
	if s.ExportFlow < 0 {
		s.ExportFlow = 0
	}
	s.Unlock()
}

type Config struct {
	U        string // username
	P        string // password
	Compress bool
	Crypt    bool
}

type Client struct {
	Cnf             *Config
	Id              int        //id
	VerifyKey       string     //verify key
	Mode            string     //bridge mode
	Addr            string     //the ip of client
	LocalAddr       string     //the local ip of client
	Remark          string     //remark
	Status          bool       //is allowed connect
	IsConnect       bool       //is the client connect
	RateLimit       int        //rate /kb
	Flow            *Flow      //flow setting
	ExportFlow      int64      //flow out
	InletFlow       int64      //flow in
	Rate            *rate.Rate //rate limit
	NoStore         bool       //no store to file
	NoDisplay       bool       //no display on web
	MaxConn         int        //the max connection num of client allow
	NowConn         int32      //the connection num of now
	WebUserName     string     //the username of web login
	WebPassword     string     //the password of web login
	WebTotpSecret   string     //the totp secret of web login
	ConfigConnAllow bool       //is allowed connected by config file
	MaxTunnelNum    int
	Version         string
	BlackIpList     []string
	CreateTime      string
	LastOnlineTime  string
	sync.RWMutex
}

func NewClient(vKey string, noStore bool, noDisplay bool) *Client {
	return &Client{
		Cnf:       new(Config),
		Id:        0,
		VerifyKey: vKey,
		Addr:      "",
		Remark:    "",
		Status:    true,
		IsConnect: false,
		RateLimit: 0,
		Flow:      new(Flow),
		Rate:      nil,
		NoStore:   noStore,
		RWMutex:   sync.RWMutex{},
		NoDisplay: noDisplay,
	}
}

func (s *Client) AddConn() {
	atomic.AddInt32(&s.NowConn, 1)
}

func (s *Client) CutConn() {
	atomic.AddInt32(&s.NowConn, -1)
}

func (s *Client) GetConn() bool {
	if s.NowConn < 0 {
		s.NowConn = 0
	}
	if s.MaxConn == 0 || int(s.NowConn) < s.MaxConn {
		s.AddConn()
		return true
	}
	return false
}

func (s *Client) HasTunnel(t *Tunnel) (tt *Tunnel, exist bool) {
	GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*Tunnel)
		if v.Client.Id == s.Id && ((v.Port == t.Port && t.Port != 0) || (v.Password == t.Password && t.Password != "")) {
			exist = true
			tt = v
			return false
		}
		return true
	})
	return
}

func (s *Client) GetTunnelNum() (num int) {
	GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*Tunnel)
		if v.Client.Id == s.Id {
			num++
		}
		return true
	})

	GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.Client.Id == s.Id {
			num++
		}
		return true
	})
	return
}

func (s *Client) HasHost(h *Host) (hh *Host, exist bool) {
	GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.Client.Id == s.Id && v.Host == h.Host && h.Location == v.Location {
			exist = true
			hh = v
			return false
		}
		return true
	})
	return
}

func (s *Client) EnsureWebPassword() {
	if s.WebTotpSecret != "" {
		if !crypt.IsValidTOTPSecret(s.WebTotpSecret) {
			s.WebTotpSecret, _ = crypt.GenerateTOTPSecret()
		}
	}
	if idx := strings.LastIndex(s.WebPassword, common.TOTP_SEQ); idx != -1 {
		secret := s.WebPassword[idx+len(common.TOTP_SEQ):]
		s.WebPassword = s.WebPassword[:idx]
		if !crypt.IsValidTOTPSecret(secret) {
			secret, _ = crypt.GenerateTOTPSecret()
		}
		s.WebTotpSecret = secret
	}
}

type Tunnel struct {
	Id              int
	Port            int
	ServerIp        string
	Mode            string
	Status          bool
	RunStatus       bool
	Client          *Client
	Ports           string
	Flow            *Flow
	NowConn         int32
	Password        string
	Remark          string
	TargetAddr      string
	TargetType      string
	WhitelistEnable bool
	Whitelist       string
	WhitelistRules  *common.WhitelistRuleSet `json:"-"`
	NoStore         bool
	IsHttp          bool
	HttpProxy       bool
	Socks5Proxy     bool
	LocalPath       string
	StripPre        string
	ReadOnly        bool
	Target          *Target
	UserAuth        *MultiAccount
	MultiAccount    *MultiAccount
	Health
	sync.RWMutex
}

func NewTunnelByHost(host *Host, port int) *Tunnel {
	return &Tunnel{
		ServerIp:     "0.0.0.0",
		Port:         port,
		Mode:         "tcp",
		Status:       !host.IsClose,
		RunStatus:    !host.IsClose,
		Client:       host.Client,
		Flow:         host.Flow,
		NoStore:      true,
		Target:       host.Target,
		UserAuth:     host.UserAuth,
		MultiAccount: host.MultiAccount,
	}
}

func (s *Tunnel) Update(t *Tunnel) {
	s.ServerIp = t.ServerIp
	s.Mode = t.Mode
	s.Password = t.Password
	s.Remark = t.Remark
	s.TargetType = t.TargetType
	s.HttpProxy = t.HttpProxy
	s.Socks5Proxy = t.Socks5Proxy
	s.WhitelistEnable = t.WhitelistEnable
	s.Whitelist = t.Whitelist
	s.WhitelistRules = t.WhitelistRules
	s.LocalPath = t.LocalPath
	s.StripPre = t.StripPre
	s.ReadOnly = t.ReadOnly
	s.Target = t.Target
	s.MultiAccount = t.MultiAccount
}

func (s *Tunnel) AddConn() {
	atomic.AddInt32(&s.NowConn, 1)
}

func (s *Tunnel) CutConn() {
	atomic.AddInt32(&s.NowConn, -1)
}

type Health struct {
	HealthCheckTimeout  int
	HealthMaxFail       int
	HealthCheckInterval int
	HealthNextTime      time.Time
	HealthMap           map[string]int
	HttpHealthUrl       string
	HealthRemoveArr     []string
	HealthCheckType     string
	HealthCheckTarget   string
	sync.RWMutex
}

type Host struct {
	Id               int
	Host             string //host
	HeaderChange     string //request header change
	RespHeaderChange string //response header change
	HostChange       string //host change
	Location         string //url router
	PathRewrite      string //url rewrite
	Remark           string //remark
	Scheme           string //http https all
	RedirectURL      string // 307
	HttpsJustProxy   bool
	TlsOffload       bool
	AutoSSL          bool
	CertType         string
	CertHash         string
	CertFile         string
	KeyFile          string
	NoStore          bool
	IsClose          bool
	AutoHttps        bool
	AutoCORS         bool
	CompatMode       bool
	Flow             *Flow
	NowConn          int32
	Client           *Client
	TargetIsHttps    bool
	Target           *Target //目标
	UserAuth         *MultiAccount
	MultiAccount     *MultiAccount
	Health           `json:"-"`
	sync.RWMutex
}

func (s *Host) Update(h *Host) {
	s.HeaderChange = h.HeaderChange
	s.RespHeaderChange = h.RespHeaderChange
	s.HostChange = h.HostChange
	s.PathRewrite = h.PathRewrite
	s.Remark = h.Remark
	s.RedirectURL = h.RedirectURL
	s.HttpsJustProxy = h.HttpsJustProxy
	s.AutoSSL = h.AutoSSL
	s.CertType = common.GetCertType(h.CertFile)
	s.CertHash = crypt.FNV1a64(h.CertType, h.CertFile, h.KeyFile)
	s.CertFile = h.CertFile
	s.KeyFile = h.KeyFile
	s.AutoHttps = h.AutoHttps
	s.AutoCORS = h.AutoCORS
	s.CompatMode = h.CompatMode
	s.TargetIsHttps = h.TargetIsHttps
	s.Target = h.Target
	s.MultiAccount = h.MultiAccount
}

func (s *Host) AddConn() {
	atomic.AddInt32(&s.NowConn, 1)
}

func (s *Host) CutConn() {
	atomic.AddInt32(&s.NowConn, -1)
}

type Target struct {
	nowIndex      int
	TargetStr     string
	TargetArr     []string
	LocalProxy    bool
	ProxyProtocol int // Proxy Protocol 配置：0=关闭, 1=v1, 2=v2
	sync.RWMutex
}

type MultiAccount struct {
	Content    string
	AccountMap map[string]string // multi account and pwd
}

func GetAccountMap(multiAccount *MultiAccount) map[string]string {
	var accountMap map[string]string
	if multiAccount == nil {
		accountMap = nil
	} else {
		accountMap = multiAccount.AccountMap
	}
	return accountMap
}

func (s *Target) GetRandomTarget() (string, error) {
	// 初始化 TargetArr 并过滤空行
	if s.TargetArr == nil {
		s.TargetStr = strings.ReplaceAll(s.TargetStr, "：", ":")
		normalized := strings.ReplaceAll(s.TargetStr, "\r\n", "\n")
		lines := strings.Split(normalized, "\n")
		for _, v := range lines {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				s.TargetArr = append(s.TargetArr, trimmed)
			}
		}
	}

	// 确保 TargetArr 中有有效内容
	if len(s.TargetArr) == 1 {
		return s.TargetArr[0], nil
	}
	if len(s.TargetArr) == 0 {
		return "", errors.New("all inward-bending targets are offline")
	}

	// 锁定并更新索引
	s.Lock()
	defer s.Unlock()
	if s.nowIndex >= len(s.TargetArr)-1 {
		s.nowIndex = -1
	}
	s.nowIndex++
	return s.TargetArr[s.nowIndex], nil
}

type Glob struct {
	BlackIpList []string
	sync.RWMutex
}
