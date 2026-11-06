package file

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/byterate"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/upstream"
)

// ACLMode: 0=off, 1=whitelist(deny-by-default), 2=blacklist(allow-by-default)
const (
	AclOff       = 0
	AclWhitelist = 1
	AclBlacklist = 2
)

// Flow is the payload traffic of a client, tunnel or host and its limits.
// The exported fields are what is persisted: InletFlow and ExportFlow give
// the traffic counted before, which the Flow takes over on first use; read
// the current values with Values. The traffic counts in a Counter, and the
// limits are kept by a Budget that stops all bytes at FlowLimit MiB and
// after TimeLimit; FlowConn charges both directions of its connection to it.
type Flow struct {
	ExportFlow int64     // outbound traffic
	InletFlow  int64     // inbound traffic
	FlowLimit  int64     // traffic limit
	TimeLimit  time.Time // expire time
	// client marks a client's flow, whose limit FlowLimitFactor scales;
	// the limits of tunnels and hosts always count the in/out traffic.
	client bool
	// started is set once the counter and the budget took over the fields.
	started bool
	// factor is the FlowLimitFactor that the budget's step is set for.
	factor int64
	// traffic counts the bytes read from the connections as its reads,
	// ExportFlow, and those written to them as its writes, InletFlow.
	traffic byterate.Counter
	budget  byterate.Budget
	sync.RWMutex
}

// MarkClient marks f as the flow of a client, see FlowLimitFactor.
func (s *Flow) MarkClient() {
	if s == nil {
		return
	}
	s.Lock()
	s.client = true
	if s.started {
		s.applyLimitLocked()
	}
	s.Unlock()
}

// startLocked makes the counter and the budget take over the persisted
// fields on first use, and follows a change of FlowLimitFactor. s must be
// locked.
func (s *Flow) startLocked() {
	if !s.started {
		s.started = true
		s.traffic.Set(s.ExportFlow, s.InletFlow)
		s.budget.SetUsed(s.InletFlow + s.ExportFlow)
		s.budget.SetExpiry(s.TimeLimit)
		s.budget.OnStep(func(_ int, used int64) {
			logs.Info("Traffic limit reached after %d bytes", used)
		})
		s.applyLimitLocked()
		return
	}
	if s.factor != s.limitFactor() {
		s.applyLimitLocked()
	}
}

func (s *Flow) limitFactor() int64 {
	if s.client {
		return FlowLimitFactor.Load()
	}
	return 1
}

// applyLimitLocked sets the budget's stop just past FlowLimit MiB, counted
// as limitFactor times the traffic: a flow exactly at its quota is still
// admitted, and the first byte over it is the last that passes.
func (s *Flow) applyLimitLocked() {
	s.factor = s.limitFactor()
	if s.FlowLimit > 0 && s.FlowLimit < math.MaxInt64>>20 {
		s.budget.SetSteps(byterate.Step{After: (s.FlowLimit<<20)/s.factor + 1, Stop: true})
	} else {
		s.budget.SetSteps()
	}
}

// Budget returns the budget that the traffic of s is charged to, nil for a
// nil s.
func (s *Flow) Budget() *byterate.Budget {
	if s == nil {
		return nil
	}
	s.Lock()
	s.startLocked()
	s.Unlock()
	return &s.budget
}

// Traffic returns the counter of s, whose reads are the outbound traffic
// and whose writes are the inbound traffic, nil for a nil s.
func (s *Flow) Traffic() *byterate.Counter {
	if s == nil {
		return nil
	}
	s.Lock()
	s.startLocked()
	s.Unlock()
	return &s.traffic
}

// Add counts in bytes of inbound and out bytes of outbound traffic that
// passed outside a FlowConn.
func (s *Flow) Add(in, out int64) {
	s.Lock()
	s.startLocked()
	s.Unlock()
	s.traffic.Add(out, in)
	_ = s.budget.Charge(in + out)
}

// MarshalJSON snapshots the persisted fields while transfers update them.
func (s *Flow) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	v := s.Values()
	return json.Marshal(struct {
		ExportFlow int64
		InletFlow  int64
		FlowLimit  int64
		TimeLimit  time.Time
	}{v.ExportFlow, v.InletFlow, v.FlowLimit, v.TimeLimit})
}

// ResetTraffic zeroes the inbound and outbound traffic.
func (s *Flow) ResetTraffic() {
	s.Lock()
	s.startLocked()
	s.InletFlow, s.ExportFlow = 0, 0
	s.traffic.Set(0, 0)
	s.budget.Reset()
	s.Unlock()
}

// SetFlowLimit sets the traffic limit in MiB, 0 for none.
func (s *Flow) SetFlowLimit(limit int64) {
	s.Lock()
	s.FlowLimit = limit
	if s.started {
		s.applyLimitLocked()
	}
	s.Unlock()
}

// SetTimeLimit sets the time limit, the zero time for none.
func (s *Flow) SetTimeLimit(limit time.Time) {
	s.Lock()
	s.TimeLimit = limit
	if s.started {
		s.budget.SetExpiry(limit)
	}
	s.Unlock()
}

// SetLimits sets the traffic limit in MiB and the time limit together.
func (s *Flow) SetLimits(flowLimit int64, timeLimit time.Time) {
	s.Lock()
	s.FlowLimit = flowLimit
	s.TimeLimit = timeLimit
	if s.started {
		s.applyLimitLocked()
		s.budget.SetExpiry(timeLimit)
	}
	s.Unlock()
}

// FlowValues is a snapshot of a Flow. Used is the traffic its limit counts:
// the inbound and outbound traffic, scaled by FlowLimitFactor for a client.
type FlowValues struct {
	InletFlow, ExportFlow, FlowLimit, Used int64
	TimeLimit                              time.Time
}

// Values returns a snapshot of the flow; a nil flow gives zero values.
func (s *Flow) Values() FlowValues {
	if s == nil {
		return FlowValues{}
	}
	s.Lock()
	defer s.Unlock()
	s.startLocked()
	out, in := s.traffic.Total()
	used := in + out
	if used > math.MaxInt64/s.factor {
		used = math.MaxInt64
	} else {
		used *= s.factor
	}
	return FlowValues{InletFlow: in, ExportFlow: out, FlowLimit: s.FlowLimit, Used: used, TimeLimit: s.TimeLimit}
}

// Over reports whether the flow is over its traffic limit (FlowLimit MiB of
// inbound and outbound traffic together, scaled for a client by
// FlowLimitFactor) or past its time limit.
func (s *Flow) Over() (traffic, expired bool) {
	if s == nil {
		return false, false
	}
	s.Lock()
	s.startLocked()
	s.Unlock()
	exp := s.budget.Expiry()
	return s.budget.Step() >= 0, !exp.IsZero() && !time.Now().Before(exp)
}

// FlowLimitFactor is what the flow limits of clients multiply the inbound
// and outbound traffic by: 1 to count the traffic, 2 to count the actual
// usage on both sides of the server. A change applies to each client flow
// at its next use.
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
	ExportFlow      FlowTotal  // outbound flow of the client's hosts and tasks
	InletFlow       FlowTotal  // inbound flow of the client's hosts and tasks
	Rate            *rate.Rate // rate limiter
	NoStore         bool       // do not store to file
	NoDisplay       bool       // do not display on web
	MaxConn         int        // max concurrent connections
	NowConn         int32      // legacy JSON snapshot; use CurrentConnections at runtime
	WebUserName     string     // web username
	WebPassword     string     // web password
	WebTotpSecret   string     // web totp secret
	ConfigConnAllow bool       // allowed by config file
	MaxTunnelNum    int
	Version         string
	SourceLists
	CreateTime        string
	LastOnlineTime    string
	ConnectionHistory *ConnectionHistory `json:",omitempty"`
	// RegisterKey, if set, lets visitors register their address for the
	// client on the registration page, as the verify key does.
	RegisterKey string
	// RegisterMaxHours bounds how long a registration made on the page
	// lasts; 0 means the server default.
	RegisterMaxHours int
	sync.RWMutex
}

// FlowTotal is a traffic total that is updated while it is read and
// marshalled; it is stored and read atomically and encodes as a JSON number.
type FlowTotal struct{ v atomic.Int64 }

func (t *FlowTotal) Load() int64   { return t.v.Load() }
func (t *FlowTotal) Store(n int64) { t.v.Store(n) }

func (t *FlowTotal) MarshalJSON() ([]byte, error) { return json.Marshal(t.v.Load()) }

func (t *FlowTotal) UnmarshalJSON(b []byte) error {
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	t.v.Store(n)
	return nil
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

// ControlGroup is the client level of global -> client -> resource admission.
func (s *Client) ControlGroup() *byterate.Group {
	if s == nil {
		return rate.RootGroup()
	}
	s.Lock()
	if s.Rate == nil {
		s.Rate = rate.NewRate(0)
	}
	g := s.Rate.Group()
	g.SetMaxConns(int64(s.MaxConn))
	s.Unlock()
	return g
}

// SetMaxConn updates the client configuration and its library cap together.
func (s *Client) SetMaxConn(limit int) {
	s.Lock()
	defer s.Unlock()
	s.MaxConn = max(limit, 0)
	if s.Rate != nil {
		s.Rate.Group().SetMaxConns(int64(s.MaxConn))
	}
}

// CurrentConnections includes all admitted connections of the client's resources.
func (s *Client) CurrentConnections() int32 {
	if s == nil || s.Rate == nil {
		return 0
	}
	return int32(s.Rate.Group().Conns())
}

// GetConn and CutConn adapt legacy callers to byterate's balanced slot API.
func (s *Client) GetConn() bool { return s.ControlGroup().Acquire() == nil }
func (s *Client) CutConn()      { s.Rate.Group().Release() }

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
	RateLimits
	MaxConn      int // concurrent connections; zero means unlimited
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
	d, err := acl.CompileDest(t.DestAclMode, t.DestAclRules, "the destination list of tunnel "+strconv.Itoa(t.Id))
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
		RateLimits:   host.RateLimits.shared(),
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
// and are expected to be replaced rather than modified. Connection reservations
// are reattached on accepted updates, so an edit cannot forget active slots.
func (t *Tunnel) Clone() *Tunnel {
	t.RLock()
	c := &Tunnel{
		RateLimits:   t.RateLimits.copyConfig(),
		MaxConn:      t.MaxConn,
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
		v := flow.Values()
		c.Flow = &Flow{ExportFlow: v.ExportFlow, InletFlow: v.InletFlow, FlowLimit: v.FlowLimit, TimeLimit: v.TimeLimit}
	}

	t.Health.RLock()
	c.HealthCheckTimeout = t.HealthCheckTimeout
	c.HealthMaxFail = t.HealthMaxFail
	c.HealthCheckInterval = t.HealthCheckInterval
	c.HttpHealthUrl = t.HttpHealthUrl
	c.HealthCheckType = t.HealthCheckType
	c.HealthCheckTarget = t.HealthCheckTarget
	t.Health.RUnlock()
	return c
}

func (t *Tunnel) Update(other *Tunnel) {
	t.SetMaxConn(other.MaxConn)
	t.SetRateLimits(other.UploadLimit, other.DownloadLimit)
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

// ControlGroup is a tunnel's leaf under its client, shared across accepted edits.
func (t *Tunnel) ControlGroup() *byterate.Group {
	g := t.RateLimiter().Group(t.Client.ControlGroup())
	t.RLock()
	g.SetMaxConns(int64(t.MaxConn))
	t.RUnlock()
	return g
}

// SetMaxConn updates the library cap without reclaiming active slots.
func (t *Tunnel) SetMaxConn(limit int) {
	t.Lock()
	t.MaxConn = max(limit, 0)
	t.rateMu.Lock()
	if t.runtimeRate != nil {
		t.runtimeRate.Group(nil).SetMaxConns(int64(max(limit, 0)))
	}
	t.rateMu.Unlock()
	t.Unlock()
}
func (t *Tunnel) GetConn() bool             { return t.ControlGroup().Acquire() == nil }
func (t *Tunnel) CutConn()                  { t.RateLimiter().Group(nil).Release() }
func (t *Tunnel) CurrentConnections() int32 { return t.RateLimits.connectionCount() }

type Health struct {
	HealthCheckTimeout  int
	HealthMaxFail       int
	HealthCheckInterval int
	HttpHealthUrl       string
	HealthCheckType     string
	HealthCheckTarget   string
	sync.RWMutex
}

type Host struct {
	RateLimits
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
	s.SetRateLimits(h.UploadLimit, h.DownloadLimit)
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

// ControlGroup is a domain's leaf under its client; byte controls share it.
func (s *Host) ControlGroup() *byterate.Group { return s.RateLimiter().Group(s.Client.ControlGroup()) }
func (s *Host) CurrentConnections() int32     { return s.RateLimits.connectionCount() }

type Target struct {
	TargetStr     string
	LocalProxy    bool
	ProxyProtocol int // 0=off, 1=v1, 2=v2

	// pool balances the addresses parsed from source, the TargetStr it was
	// last synchronized with. Both are guarded by mu.
	mu     sync.Mutex
	pool   *upstream.Pool[string]
	source string
}

// ErrNoTarget is returned when no target address can take a connection.
var ErrNoTarget = errors.New("all inward-bending targets are offline")

// errTargetDown records that the client reported a target unhealthy.
var errTargetDown = errors.New("target reported unhealthy by the client")

// targetOutlier ejects a target address for a while after repeated failed
// dials, so that connections go to the others meanwhile. It counts only
// dials whose result is known, those the server makes itself.
var targetOutlier = upstream.OutlierConfig{
	ConsecutiveFailures: 5,
	BaseEjection:        30 * time.Second,
	MaxEjection:         5 * time.Minute,
}

func parseTargets(targetStr string) []string {
	return common.TrimArr(common.SplitLines(strings.ReplaceAll(targetStr, "：", ":")))
}

// targetPool returns the pool of the configured addresses, synchronizing
// it first if TargetStr changed. An address listed several times gets a
// matching weight. Addresses kept across a change keep their health. A
// failed dial is retried on each other address once.
func (s *Target) targetPool() *upstream.Pool[string] {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil && s.source == s.TargetStr {
		return s.pool
	}
	if s.pool == nil {
		s.pool = upstream.New[string](upstream.Config{Outlier: targetOutlier})
	}
	s.source = s.TargetStr
	addrs := parseTargets(s.TargetStr)
	targets := make([]upstream.Target[string], 0, len(addrs))
	index := make(map[string]int, len(addrs))
	for _, addr := range addrs {
		if i, ok := index[addr]; ok {
			targets[i].Weight++
			continue
		}
		index[addr] = len(targets)
		targets = append(targets, upstream.Target[string]{ID: addr, Weight: 1, Value: addr})
	}
	if err := s.pool.Sync(targets); err != nil {
		logs.Warn("invalid target list %q: %v", s.TargetStr, err)
	}
	_ = s.pool.SetConfig(upstream.Config{Outlier: targetOutlier, Retry: upstream.RetryConfig{Attempts: len(targets)}})
	return s.pool
}

// Dial connects to a target: it calls dial with the next healthy target
// address, in weighted round-robin order, and again with another address
// each time dial fails for that address, trying each address at most once.
// dial reports whether it reached the address itself; only such results
// judge the address, while a failure on the way to it, such as on the link
// to the client that dials it, ends Dial without a retry. Dial returns
// ErrNoTarget if no address can be tried, and otherwise dial's last error.
func (s *Target) Dial(dial func(addr string) (direct bool, err error)) error {
	req := s.targetPool().Request("")
	picked := false
	err := req.Do(context.Background(), func(l *upstream.Lease[string]) (bool, error) {
		picked = true
		direct, err := dial(l.Value())
		if !direct {
			l.Release(upstream.Neutral)
			return false, err
		}
		l.Release(upstream.OutcomeOf(err))
		return err != nil, err
	})
	if !picked {
		return ErrNoTarget
	}
	return err
}

// SetHealthy takes addr out of the rotation or puts it back, and reports
// whether addr is one of the configured targets. The client's checks
// override failed dials: an address it reports healthy again is no longer
// ejected for them.
func (s *Target) SetHealthy(addr string, healthy bool) bool {
	p := s.targetPool()
	b, ok := p.Backend(addr)
	if !ok {
		return false
	}
	h, r := upstream.HealthUp, upstream.CheckResult{Time: time.Now()}
	if !healthy {
		h, r.Err = upstream.HealthDown, errTargetDown
	} else if b.State() == upstream.StateUnhealthy {
		p.ResetOutlier(addr)
	}
	return p.ReportCheck(b, h, r)
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
	MaxConn int // total concurrent forwarded connections; zero means unlimited
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

// ApplyLimits applies the global bandwidth and connection limits.
func (g *Glob) ApplyLimits() {
	if g != nil {
		rate.SetGlobalLimit(int64(g.RateLimit) * 1024)
		SetGlobalMaxConn(g.MaxConn)
	}
}

// CurrentRates reports the client's visitor upload and download speeds.
// The existing RateLimit continues to limit their combined throughput.
func (s *Client) CurrentRates() (int64, int64) {
	if s == nil {
		return 0, 0
	}
	return s.Rate.CurrentRates()
}
