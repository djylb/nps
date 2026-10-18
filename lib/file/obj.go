package file

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

// ACLMode: 0=off, 1=whitelist(deny-by-default), 2=blacklist(allow-by-default)
const (
	AclOff       = 0
	AclWhitelist = 1
	AclBlacklist = 2
)

type Flow struct {
	ExportFlow int64     // outbound traffic
	InletFlow  int64     // inbound traffic
	FlowLimit  int64     // traffic limit
	TimeLimit  time.Time // expire time
	// client marks a client's flow, whose limit FlowLimitFactor scales;
	// the limits of tunnels and hosts always count the in/out traffic.
	client bool
	sync.RWMutex
}

// MarkClient marks f as the flow of a client, see FlowLimitFactor.
func (s *Flow) MarkClient() {
	if s == nil {
		return
	}
	s.Lock()
	s.client = true
	s.Unlock()
}

func (s *Flow) Add(in, out int64) {
	s.Lock()
	s.InletFlow += in
	s.ExportFlow += out
	s.Unlock()
}

// ResetTraffic zeroes the inbound and outbound traffic.
func (s *Flow) ResetTraffic() {
	s.Lock()
	s.InletFlow = 0
	s.ExportFlow = 0
	s.Unlock()
}

// SetFlowLimit sets the traffic limit in MiB, 0 for none.
func (s *Flow) SetFlowLimit(limit int64) {
	s.Lock()
	s.FlowLimit = limit
	s.Unlock()
}

// SetTimeLimit sets the time limit, the zero time for none.
func (s *Flow) SetTimeLimit(limit time.Time) {
	s.Lock()
	s.TimeLimit = limit
	s.Unlock()
}

// SetLimits sets the traffic limit in MiB and the time limit together.
func (s *Flow) SetLimits(flowLimit int64, timeLimit time.Time) {
	s.Lock()
	s.FlowLimit = flowLimit
	s.TimeLimit = timeLimit
	s.Unlock()
}

// AddOver adds like Add and reports, under the same lock, whether the flow
// is then over its traffic limit or past its time limit.
func (s *Flow) AddOver(in, out int64) (traffic, expired bool) {
	s.Lock()
	s.InletFlow += in
	s.ExportFlow += out
	traffic, expired = s.overLocked()
	s.Unlock()
	return traffic, expired
}

// FlowValues is a snapshot of a Flow. Used is the traffic its limit counts:
// the inbound and outbound traffic, scaled by FlowLimitFactor for a client.
type FlowValues struct {
	InletFlow, ExportFlow, FlowLimit, Used int64
	TimeLimit                              time.Time
}

// Values returns a snapshot of the flow, taken under its lock; a nil flow
// gives zero values.
func (s *Flow) Values() FlowValues {
	if s == nil {
		return FlowValues{}
	}
	s.RLock()
	defer s.RUnlock()
	used := s.InletFlow + s.ExportFlow
	if s.client {
		used *= FlowLimitFactor.Load()
	}
	return FlowValues{InletFlow: s.InletFlow, ExportFlow: s.ExportFlow, FlowLimit: s.FlowLimit, Used: used, TimeLimit: s.TimeLimit}
}

// Over reports whether the flow is over its traffic limit (FlowLimit MiB of
// inbound and outbound traffic together) or past its time limit. The fields
// are read under the lock, so that a 64-bit counter is never seen half
// written on 32-bit platforms.
func (s *Flow) Over() (traffic, expired bool) {
	s.RLock()
	traffic, expired = s.overLocked()
	s.RUnlock()
	return traffic, expired
}

// FlowLimitFactor is what the flow limits of clients multiply the inbound
// and outbound traffic by: 1 to count the traffic, 2 to count the actual
// usage on both sides of the server.
var FlowLimitFactor atomic.Int64

func init() { FlowLimitFactor.Store(1) }

// SetFlowLimitActual makes the flow limits of clients count the actual
// usage, twice the traffic, or the traffic itself.
func SetFlowLimitActual(actual bool) {
	if actual {
		FlowLimitFactor.Store(2)
	} else {
		FlowLimitFactor.Store(1)
	}
}

func (s *Flow) overLocked() (traffic, expired bool) {
	used := s.ExportFlow + s.InletFlow
	if s.client {
		used *= FlowLimitFactor.Load()
	}
	traffic = s.FlowLimit > 0 && (s.FlowLimit<<20) < used
	expired = !s.TimeLimit.IsZero() && s.TimeLimit.Before(time.Now())
	return traffic, expired
}

type Config struct {
	U        string // username
	P        string // password
	Compress bool
	Crypt    bool
}

type Client struct {
	Cnf             *Config
	Id              int        // id
	VerifyKey       string     // verify key
	Mode            string     // bridge mode
	Addr            string     // client ip
	LocalAddr       string     // client local ip
	Remark          string     // remark
	Status          bool       // allowed to connect
	IsConnect       bool       // connected now
	RateLimit       int        // rate limit (KB/s)
	Flow            *Flow      // flow
	ExportFlow      int64      // outbound flow
	InletFlow       int64      // inbound flow
	Rate            *rate.Rate // rate limiter
	NoStore         bool       // do not store to file
	NoDisplay       bool       // do not display on web
	MaxConn         int        // max concurrent connections
	NowConn         int32      // current connections
	WebUserName     string     // web username
	WebPassword     string     // web password
	WebTotpSecret   string     // web totp secret
	ConfigConnAllow bool       // allowed by config file
	MaxTunnelNum    int
	Version         string
	SourceLists
	CreateTime     string
	LastOnlineTime string
	// RegisterKey, if set, lets visitors register their address for the
	// client on the registration page, as the verify key does.
	RegisterKey string
	// RegisterMaxHours bounds how long a registration made on the page
	// lasts; 0 means the server default.
	RegisterMaxHours int
	sync.RWMutex
}

// RegisterHoursLimit returns how long a registration made on the page may
// last at most, in hours: the client's limit, or def when it sets none.
func (s *Client) RegisterHoursLimit(def int) int {
	if s.RegisterMaxHours > 0 {
		return s.RegisterMaxHours
	}
	return def
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

// CutConn releases a connection taken by GetConn or AddConn; the count never
// drops below zero.
func (s *Client) CutConn() {
	for {
		n := atomic.LoadInt32(&s.NowConn)
		if n <= 0 {
			return
		}
		if atomic.CompareAndSwapInt32(&s.NowConn, n, n-1) {
			return
		}
	}
}

// GetConn takes a connection slot, reporting false when MaxConn is reached.
func (s *Client) GetConn() bool {
	for {
		n := atomic.LoadInt32(&s.NowConn)
		next := n + 1
		if n < 0 {
			next = 1
		}
		if s.MaxConn != 0 && int(next) > s.MaxConn {
			return false
		}
		if atomic.CompareAndSwapInt32(&s.NowConn, n, next) {
			return true
		}
	}
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
	if idx := strings.LastIndex(s.WebPassword, common.TOTPSeq); idx != -1 {
		secret := s.WebPassword[idx+len(common.TOTPSeq):]
		s.WebPassword = s.WebPassword[:idx]
		if !crypt.IsValidTOTPSecret(secret) {
			secret, _ = crypt.GenerateTOTPSecret()
		}
		s.WebTotpSecret = secret
	}
}

type Tunnel struct {
	Id           int
	Port         int
	ServerIp     string
	Mode         string
	Status       bool
	RunStatus    bool
	Client       *Client
	Ports        string
	Flow         *Flow
	NowConn      int32
	Password     string
	Remark       string
	TargetAddr   string
	TargetType   string
	DestAclMode  int    // 0=off, 1=whitelist, 2=blacklist
	DestAclRules string // one rule per line
	destAcl      *acl.Dest
	SourceLists
	NoStore      bool
	IsHttp       bool
	HttpProxy    bool
	Socks5Proxy  bool
	LocalPath    string
	StripPre     string
	ReadOnly     bool
	Target       *Target
	UserAuth     *MultiAccount
	MultiAccount *MultiAccount
	Health
	sync.RWMutex
}

// CompileDestACL compiles the destination list. Rules that cannot be used
// are logged and skipped.
func (t *Tunnel) CompileDestACL() {
	if t == nil {
		return
	}
	t.Lock()
	defer t.Unlock()
	t.compileDestACLLocked()
}

func (t *Tunnel) compileDestACLLocked() {
	if t.DestAclMode != AclOff && t.DestAclMode != AclWhitelist && t.DestAclMode != AclBlacklist {
		t.DestAclMode = AclOff
	}
	d, err := acl.CompileDest(t.DestAclMode, t.DestAclRules)
	if err != nil {
		logs.Warn("Destination list of tunnel %d: rules skipped: %v", t.Id, err)
	}
	t.destAcl = d
}

// AllowsDestination reports whether the destination list lets the tunnel
// reach addr, a host or host:port.
func (t *Tunnel) AllowsDestination(addr string) bool {
	if t == nil {
		return true
	}
	t.RLock()
	mode, d := t.DestAclMode, t.destAcl
	t.RUnlock()
	if mode != AclWhitelist && mode != AclBlacklist {
		return true
	}
	if d == nil {
		t.Lock()
		if t.destAcl == nil {
			t.compileDestACLLocked()
		}
		d = t.destAcl
		t.Unlock()
	}
	return d.Allows(addr)
}

// ConfigTaskPorts expands a task delivered by a client configuration into the
// server ports it is opened on and, for multi-port tcp/udp tasks, the matching
// target ports. The server reports one add status per returned port, or a
// single failure status when ok is false, so both sides use this to stay in
// step on the configuration connection.
func ConfigTaskPorts(t *Tunnel) (ports, targets []int, ok bool) {
	if t == nil || t.Target == nil {
		return nil, nil, false
	}
	ports = common.GetPorts(t.Ports)
	targets = common.GetPorts(t.Target.TargetStr)
	if len(ports) > 1 && (t.Mode == "tcp" || t.Mode == "udp") && len(ports) != len(targets) {
		return nil, nil, false
	} else if t.Mode == "secret" || t.Mode == "p2p" {
		ports = append(ports, 0)
	}
	if t.Mode == "file" && len(ports) == 0 {
		ports = append(ports, 0)
	}
	if len(ports) == 0 {
		return nil, nil, false
	}
	return ports, targets, true
}

func NewTunnelByHost(host *Host, port int) *Tunnel {
	t := &Tunnel{
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
	t.CopySourceLists(&host.SourceLists)
	return t
}

// Clone returns a copy of t that can be edited and then stored with
// UpdateTask without touching t until the update is accepted. Flow and the
// health state are copied; Client, Target and the account settings are shared
// and are expected to be replaced rather than modified.
func (t *Tunnel) Clone() *Tunnel {
	t.RLock()
	c := &Tunnel{
		Id:           t.Id,
		Port:         t.Port,
		ServerIp:     t.ServerIp,
		Mode:         t.Mode,
		Status:       t.Status,
		RunStatus:    t.RunStatus,
		Client:       t.Client,
		Ports:        t.Ports,
		NowConn:      atomic.LoadInt32(&t.NowConn),
		Password:     t.Password,
		Remark:       t.Remark,
		TargetAddr:   t.TargetAddr,
		TargetType:   t.TargetType,
		DestAclMode:  t.DestAclMode,
		DestAclRules: t.DestAclRules,
		destAcl:      t.destAcl,
		NoStore:      t.NoStore,
		IsHttp:       t.IsHttp,
		HttpProxy:    t.HttpProxy,
		Socks5Proxy:  t.Socks5Proxy,
		LocalPath:    t.LocalPath,
		StripPre:     t.StripPre,
		ReadOnly:     t.ReadOnly,
		Target:       t.Target,
		UserAuth:     t.UserAuth,
		MultiAccount: t.MultiAccount,
	}
	c.CopySourceLists(&t.SourceLists)
	flow := t.Flow
	t.RUnlock()

	if flow != nil {
		flow.RLock()
		c.Flow = &Flow{ExportFlow: flow.ExportFlow, InletFlow: flow.InletFlow, FlowLimit: flow.FlowLimit, TimeLimit: flow.TimeLimit}
		flow.RUnlock()
	}

	t.Health.RLock()
	c.HealthCheckTimeout = t.HealthCheckTimeout
	c.HealthMaxFail = t.HealthMaxFail
	c.HealthCheckInterval = t.HealthCheckInterval
	c.HealthNextTime = t.HealthNextTime
	if t.HealthMap != nil {
		c.HealthMap = make(map[string]int, len(t.HealthMap))
		for k, v := range t.HealthMap {
			c.HealthMap[k] = v
		}
	}
	c.HttpHealthUrl = t.HttpHealthUrl
	c.HealthRemoveArr = append([]string(nil), t.HealthRemoveArr...)
	c.HealthCheckType = t.HealthCheckType
	c.HealthCheckTarget = t.HealthCheckTarget
	t.Health.RUnlock()
	return c
}

func (t *Tunnel) Update(other *Tunnel) {
	t.ServerIp = other.ServerIp
	t.Mode = other.Mode
	t.Password = other.Password
	t.Remark = other.Remark
	t.TargetType = other.TargetType
	t.HttpProxy = other.HttpProxy
	t.Socks5Proxy = other.Socks5Proxy
	t.DestAclMode = other.DestAclMode
	t.DestAclRules = other.DestAclRules
	t.destAcl = other.destAcl
	t.CopySourceLists(&other.SourceLists)
	t.LocalPath = other.LocalPath
	t.StripPre = other.StripPre
	t.ReadOnly = other.ReadOnly
	t.Target = other.Target
	t.MultiAccount = other.MultiAccount
}

func (t *Tunnel) AddConn() {
	atomic.AddInt32(&t.NowConn, 1)
}

func (t *Tunnel) CutConn() {
	atomic.AddInt32(&t.NowConn, -1)
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
	Host             string // host
	HeaderChange     string // request header change
	RespHeaderChange string // response header change
	HostChange       string // host change
	Location         string // url router
	PathRewrite      string // url rewrite
	Remark           string // remark
	Scheme           string // http/https/all
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
	Target           *Target
	UserAuth         *MultiAccount
	MultiAccount     *MultiAccount
	SourceLists
	Health `json:"-"`
	sync.RWMutex
}

// HasCert reports whether the entry has a certificate configured.
func (s *Host) HasCert() bool {
	return s.CertFile != "" && s.KeyFile != ""
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
	s.CopySourceLists(&h.SourceLists)
}

func (s *Host) AddConn() {
	atomic.AddInt32(&s.NowConn, 1)
}

func (s *Host) CutConn() {
	atomic.AddInt32(&s.NowConn, -1)
}

type Target struct {
	TargetStr     string
	LocalProxy    bool
	ProxyProtocol int // 0=off, 1=v1, 2=v2

	// targets is the runtime list of healthy targets parsed from TargetStr;
	// source is the TargetStr it was parsed from. Both, and nowIndex, are
	// guarded by the embedded mutex.
	targets  []string
	source   string
	nowIndex int
	sync.RWMutex
}

func parseTargets(targetStr string) []string {
	return common.TrimArr(common.SplitLines(strings.ReplaceAll(targetStr, "：", ":")))
}

// syncTargetsLocked (re)builds the target list whenever TargetStr changed.
// The caller must hold the write lock.
func (s *Target) syncTargetsLocked() {
	if s.targets == nil || s.source != s.TargetStr {
		s.source = s.TargetStr
		s.targets = parseTargets(s.TargetStr)
		s.nowIndex = 0
	}
}

func (s *Target) GetRandomTarget() (string, error) {
	s.Lock()
	defer s.Unlock()
	s.syncTargetsLocked()
	if len(s.targets) == 0 {
		return "", errors.New("all inward-bending targets are offline")
	}
	if s.nowIndex >= len(s.targets) {
		s.nowIndex = 0
	}
	target := s.targets[s.nowIndex]
	s.nowIndex++
	return target, nil
}

// RemoveTarget takes addr out of the rotation and reports whether it was
// present.
func (s *Target) RemoveTarget(addr string) bool {
	s.Lock()
	defer s.Unlock()
	s.syncTargetsLocked()
	for i, v := range s.targets {
		if v == addr {
			targets := make([]string, 0, len(s.targets)-1)
			targets = append(targets, s.targets[:i]...)
			s.targets = append(targets, s.targets[i+1:]...)
			return true
		}
	}
	return false
}

// RestoreTarget puts addr back into the rotation if it is a configured target
// that is currently out of it, and reports whether it was restored.
func (s *Target) RestoreTarget(addr string) bool {
	s.Lock()
	defer s.Unlock()
	s.syncTargetsLocked()
	if common.IsArrContains(s.targets, addr) || !common.IsArrContains(parseTargets(s.TargetStr), addr) {
		return false
	}
	s.targets = append(s.targets, addr)
	return true
}

// HasTarget reports whether addr is one of the configured targets.
func (s *Target) HasTarget(addr string) bool {
	s.RLock()
	defer s.RUnlock()
	return common.IsArrContains(parseTargets(s.TargetStr), addr)
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

type Glob struct {
	SourceLists
	// RateLimit limits all forwarded traffic together, in KB/s; 0 means
	// unlimited.
	RateLimit int
	// InletFlow and ExportFlow are the bytes forwarded in total, from
	// visitors to clients and back, as of the last store; the running
	// totals are kept by package rate.
	InletFlow  int64
	ExportFlow int64
	sync.RWMutex
}

// ApplyLimits makes the server use g's global rate limit.
func (g *Glob) ApplyLimits() {
	if g != nil {
		rate.SetGlobalLimit(int64(g.RateLimit) * 1024)
	}
}
