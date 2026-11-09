package model

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/byterate"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/slogx"
	"github.com/djylb/upstream"
)

var log = slogx.Module("store")

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
	// owner holds the fields naming the entry of the flow (client, tunnel,
	// host) for its quota_reached events.
	owner atomic.Pointer[[]any]
	sync.RWMutex
}

// SetOwner names the entry of f in its quota_reached events, as fields such
// as "client", 3 or "tunnel", 7, "client", 3.
func (s *Flow) SetOwner(args ...any) {
	if s != nil {
		s.owner.Store(&args)
	}
}

// quotaReached writes the quota_reached event of f, kind flow or expire.
func (s *Flow) quotaReached(kind string, args ...any) {
	var fields []any
	if o := s.owner.Load(); o != nil {
		fields = append(fields, *o...)
	}
	fields = append(fields, "kind", kind)
	slogx.Emit(context.Background(), log, &logevent.QuotaReached, append(fields, args...)...)
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
			s.quotaReached("flow", "bytes", used)
		})
		s.budget.AfterExpiry(func() { s.quotaReached("expire") })
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
	// CompressAlg is the algorithm of Compress for npc on the core
	// protocol (flate, zstd, snappy); empty is snappy, the old protocol's
	// compression (§7.1).
	CompressAlg string `json:",omitempty"`
	Crypt       bool
	// SchedulerDown is how nps schedules sending to this client on the core
	// protocol (connprofile.Schedulers); empty is affinity.
	SchedulerDown string `json:",omitempty"`
	// SchedulerUp is the scheduler npc is told to use for sending to nps
	// (connprofile.Schedulers); empty tells nothing and npc keeps its own.
	SchedulerUp string `json:",omitempty"`
	// Shaping turns on traffic shaping for this client (off by default); it
	// takes effect only with a shaping source configured on the server.
	Shaping bool `json:",omitempty"`
}

// ShapedStats are the shaping statistics of a client, as listed.
type ShapedStats struct {
	Wire          uint64 // wire bytes added
	StallMs       int64  // waits applied, in milliseconds
	Injected      uint64 // records sent for shaping only
	Clamped       uint64
	InjectDropped uint64
	Faults        uint64 // shaper calls that failed
}

// Tunnel and host override of the compression of a client; "" follows the
// client.
const (
	CompressDefault = ""
	CompressOff     = "off"
	CompressFlate   = "flate"
	CompressZstd    = "zstd"
	CompressSnappy  = "snappy"
)

var (
	compressOnlyFlate  = []string{CompressFlate}
	compressOnlyZstd   = []string{CompressZstd}
	compressOnlySnappy = []string{CompressSnappy}
)

// NormalizeCompress returns alg when it is a compression override (the
// Compress* constants, "" following the client) and "" otherwise.
func NormalizeCompress(alg string) string {
	switch alg = strings.ToLower(strings.TrimSpace(alg)); alg {
	case CompressOff, CompressFlate, CompressZstd, CompressSnappy:
		return alg
	}
	return CompressDefault
}

// NormalizeClientCompress returns alg when it is an algorithm of a client's
// Compress, "" (snappy) otherwise.
func NormalizeClientCompress(alg string) string {
	if alg = NormalizeCompress(alg); alg == CompressOff {
		return CompressDefault
	}
	return alg
}

func compressOf(alg string) []string {
	switch alg {
	case CompressFlate:
		return compressOnlyFlate
	case CompressZstd:
		return compressOnlyZstd
	case CompressSnappy:
		return compressOnlySnappy
	}
	return nil
}

// SchedulerDownName is the scheduler of sending to the client, "" on a client
// without a configuration.
func (c *Config) SchedulerDownName() string {
	if c == nil {
		return ""
	}
	return c.SchedulerDown
}

// SchedulerUpName is the scheduler npc is told to send with, "" for none.
func (c *Config) SchedulerUpName() string {
	if c == nil {
		return ""
	}
	return c.SchedulerUp
}

// ParseClientCompress reads the compression of a client from a form or API
// call. compress is the single form choice: 0/1 (the old switch), or an
// algorithm, which turns compression on with it. alg refines an on switch;
// "off" in either place turns compression off. The result is the Compress and
// CompressAlg of the client's Config; off leaves no algorithm.
func ParseClientCompress(compress, alg string) (bool, string) {
	compress = strings.ToLower(strings.TrimSpace(compress))
	if NormalizeClientCompress(compress) != "" {
		return true, compress
	}
	if on, _ := textutil.ParseBool(compress); !on { // also "off" and ""
		return false, ""
	}
	if strings.EqualFold(strings.TrimSpace(alg), CompressOff) {
		return false, ""
	}
	return true, NormalizeClientCompress(alg)
}

// CompressList is the default compression of a client: algorithms in order of
// preference, nil for none (§7). The result is shared and not modified.
func (c *Config) CompressList() []string {
	if c == nil || !c.Compress {
		return nil
	}
	if c.CompressAlg == "" {
		return compressOnlySnappy
	}
	return compressOf(c.CompressAlg)
}

// ResolveCompress is the compression of a stream of a tunnel or host:
// override, or the client's when it is CompressDefault.
func ResolveCompress(c *Client, override string) []string {
	switch override {
	case CompressDefault:
		if c == nil {
			return nil
		}
		return c.Cnf.CompressList()
	case CompressOff:
		return nil
	}
	return compressOf(override)
}

// KeyKind is the scope of a client's vkey on the bridge.
type KeyKind uint8

const (
	KeyControl KeyKind = iota // a stored client: every workflow
	KeyPublic                 // public_vkey: every workflow but the main one
	KeyVisitor                // visitor_vkey: visiting secret and P2P tasks
)

type Client struct {
	Cnf       *Config
	Id        int    // id
	VerifyKey string // verify key
	Mode      string // bridge mode
	Addr      string // client ip
	LocalAddr string // client local ip
	Remark    string // remark
	Status    bool   // allowed to connect
	// LaunchForm is the start command form the web page saved, opaque to the
	// server; it holds no vkey.
	LaunchForm string `json:",omitempty"`
	IsConnect  bool   // connected now
	CanUpdate  bool   `json:",omitempty"` // a connected npc can be updated from the web; set when listed
	// Shaped sums the shaping statistics of the connected npc in the
	// direction the server sends; set when listed for a client with shaping.
	Shaped          *ShapedStats `json:",omitempty"`
	RateLimit       int          // rate limit (KB/s)
	Flow            *Flow        // flow
	ExportFlow      FlowTotal    // outbound flow of the client's hosts and tasks
	InletFlow       FlowTotal    // inbound flow of the client's hosts and tasks
	Rate            *rate.Rate   // rate limiter
	NoStore         bool         // do not store to file
	NoDisplay       bool         // do not display on web
	KeyKind         KeyKind      `json:"-"` // what the vkey may do on the bridge
	MaxConn         int          // max concurrent connections
	NowConn         int32        // legacy JSON snapshot; use CurrentConnections at runtime
	WebUserName     string       // web username
	WebPassword     string       // web password
	WebTotpSecret   string       // web totp secret
	ConfigConnAllow bool         // allowed by config file
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

func (s *Client) EnsureWebPassword() {
	if s.WebTotpSecret != "" {
		if !crypt.IsValidTOTPSecret(s.WebTotpSecret) {
			s.WebTotpSecret, _ = crypt.GenerateTOTPSecret()
		}
	}
	if idx := strings.LastIndex(s.WebPassword, totpSeq); idx != -1 {
		secret := s.WebPassword[idx+len(totpSeq):]
		s.WebPassword = s.WebPassword[:idx]
		if !crypt.IsValidTOTPSecret(secret) {
			secret, _ = crypt.GenerateTOTPSecret()
		}
		s.WebTotpSecret = secret
	}
}

type Tunnel struct {
	// hostRoute is the domain of the host entry a tunnel built by
	// NewTunnelByHost serves; see LogRoute.
	hostRoute string

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
	Routes       *RouteSet `json:"-"`
	UserAuth     *MultiAccount
	MultiAccount *MultiAccount
	// HealthConfig is the check the server gives the client; replaced, never
	// modified, under the entry's lock.
	HealthConfig *HealthCheck `json:",omitempty"`
	sync.RWMutex
	// Compress overrides the client's compression (Compress* constants).
	Compress string `json:",omitempty"`
	// ServiceKind and ServiceName are what npc reports a virtual backend
	// under (§8). The server shows them and treats them as opaque; together
	// they decide the identifier in Target, so neither is ever edited.
	ServiceKind string `json:",omitempty"`
	ServiceName string `json:",omitempty"`
	// Generated marks a tunnel converted from a file tunnel with a port.
	Generated bool `json:",omitempty"`
	// LaunchForm is the start command form the web page saved, opaque to the
	// server; it holds no access key.
	LaunchForm string `json:",omitempty"`
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
		log.Warn("destination list rules skipped", "tunnel", t.Id, "err", err)
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
	ports = netaddr.GetPorts(t.Ports)
	targets = netaddr.GetPorts(t.Target.TargetStr)
	if len(ports) > 1 && (t.Mode == "tcp" || t.Mode == "udp") {
		if _, ok := MatchTargetPorts(ports, targets, false); !ok {
			return nil, nil, false
		}
	} else if t.Mode == "secret" {
		ports = append(ports, 0)
	}
	if len(ports) == 0 {
		return nil, nil, false
	}
	return ports, targets, true
}

// MatchTargetPorts returns the target port of each of ports. The targets pair
// with the ports one to one; with single set, one target serves every port.
// ok is false for any other count.
func MatchTargetPorts(ports, targets []int, single bool) (matched []int, ok bool) {
	switch {
	case len(targets) == len(ports):
		return targets, true
	case single && len(targets) == 1:
		matched = make([]int, len(ports))
		for i := range matched {
			matched[i] = targets[0]
		}
		return matched, true
	}
	return nil, false
}

// PortTaskTarget is the target of a tunnel opened for one port of a
// multi-port task: the port on host, or the bare port when host is empty.
func PortTaskTarget(host string, port int) string {
	if host != "" {
		return host + ":" + strconv.Itoa(port)
	}
	return strconv.Itoa(port)
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
		Routes:       host.Routes,
		Compress:     host.Compress,
		UserAuth:     host.UserAuth,
		MultiAccount: host.MultiAccount,
		hostRoute:    host.Host,
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
		Compress:     t.Compress,
		ServiceKind:  t.ServiceKind,
		ServiceName:  t.ServiceName,
		Generated:    t.Generated,
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
		Routes:       t.Routes,
		IsHttp:       t.IsHttp,
		HttpProxy:    t.HttpProxy,
		Socks5Proxy:  t.Socks5Proxy,
		LocalPath:    t.LocalPath,
		StripPre:     t.StripPre,
		ReadOnly:     t.ReadOnly,
		Target:       t.Target,
		UserAuth:     t.UserAuth,
		MultiAccount: t.MultiAccount,
		HealthConfig: t.HealthConfig,
		LaunchForm:   t.LaunchForm,
	}
	c.CopySourceLists(&t.SourceLists)
	flow := t.Flow
	t.RUnlock()

	if flow != nil {
		v := flow.Values()
		c.Flow = &Flow{ExportFlow: v.ExportFlow, InletFlow: v.InletFlow, FlowLimit: v.FlowLimit, TimeLimit: v.TimeLimit}
	}

	return c
}

func (t *Tunnel) Update(other *Tunnel) {
	t.SetMaxConn(other.MaxConn)
	t.SetRateLimits(other.UploadLimit, other.DownloadLimit)
	t.Lock()
	defer t.Unlock()
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

// adoptFlow keeps counting on the flow of previous, which active connections
// add to, with the limits of t.
// AdoptRuntime makes t, an accepted edit of previous, take over the rate
// buckets, connection slots and flow of previous, so that the connections
// already open keep counting against the edited tunnel.
func (t *Tunnel) AdoptRuntime(previous *Tunnel) {
	t.RateLimits.adopt(&previous.RateLimits)
	t.ControlGroup()
	t.adoptFlow(previous)
}

func (t *Tunnel) adoptFlow(previous *Tunnel) {
	if t.Flow == nil || previous.Flow == nil || t.Flow == previous.Flow {
		return
	}
	v := t.Flow.Values()
	previous.Flow.SetLimits(v.FlowLimit, v.TimeLimit)
	t.Flow = previous.Flow
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

// HealthCheck is what can be set for a health check given by the server.
type HealthCheck struct {
	Enabled  bool
	Type     string // tcp, http or https
	Path     string // of an http or https check
	Interval int    // seconds
	Timeout  int    // seconds
	MaxFail  int
}

// Normalized returns c with what is unset or unusable replaced by defaults.
func (c HealthCheck) Normalized() HealthCheck {
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	if c.Type != "http" && c.Type != "https" {
		c.Type = "tcp"
	}
	if c.Path = strings.TrimSpace(c.Path); c.Type == "tcp" {
		c.Path = ""
	} else if !strings.HasPrefix(c.Path, "/") {
		c.Path = "/" + c.Path
	}
	if c.Interval <= 0 {
		c.Interval = 10
	}
	if c.Timeout <= 0 {
		c.Timeout = 3
	}
	if c.MaxFail <= 0 {
		c.MaxFail = 3
	}
	return c
}

// HealthCheck returns the settings of the entry's check, with defaults.
func (t *Tunnel) HealthCheck() HealthCheck {
	t.RLock()
	defer t.RUnlock()
	return t.HealthConfig.orDefault()
}

// SetHealthCheck sets the entry's check.
func (t *Tunnel) SetHealthCheck(c HealthCheck) {
	c = c.Normalized()
	t.Lock()
	defer t.Unlock()
	t.HealthConfig = &c
}

// HealthCheck returns the settings of the entry's check, with defaults.
func (s *Host) HealthCheck() HealthCheck {
	s.RLock()
	defer s.RUnlock()
	return s.HealthConfig.orDefault()
}

// SetHealthCheck sets the entry's check.
func (s *Host) SetHealthCheck(c HealthCheck) {
	c = c.Normalized()
	s.Lock()
	defer s.Unlock()
	s.HealthConfig = &c
}

func (c *HealthCheck) orDefault() HealthCheck {
	if c == nil {
		return HealthCheck{}.Normalized()
	}
	return *c
}

type Host struct {
	RateLimits
	// Compress overrides the client's compression (Compress* constants).
	Compress         string `json:",omitempty"`
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
	MaxConn          int // concurrent HTTP requests and HTTPS connections; zero means unlimited
	NowConn          int32
	Client           *Client
	TargetIsHttps    bool
	Target           *Target
	Routes           *RouteSet `json:"-"`
	UserAuth         *MultiAccount
	MultiAccount     *MultiAccount
	SourceLists
	// HealthConfig is as in Tunnel.
	HealthConfig *HealthCheck `json:",omitempty"`
	sync.RWMutex
}

// HasCert reports whether the entry has a certificate configured.
func (s *Host) HasCert() bool {
	return s.CertFile != "" && s.KeyFile != ""
}

func (s *Host) Update(h *Host) {
	s.SetMaxConn(h.MaxConn)
	s.SetRateLimits(h.UploadLimit, h.DownloadLimit)
	s.Lock()
	defer s.Unlock()
	s.HeaderChange = h.HeaderChange
	s.RespHeaderChange = h.RespHeaderChange
	s.HostChange = h.HostChange
	s.PathRewrite = h.PathRewrite
	s.Remark = h.Remark
	s.RedirectURL = h.RedirectURL
	s.HttpsJustProxy = h.HttpsJustProxy
	s.AutoSSL = h.AutoSSL
	s.CertType = crypt.GetCertType(h.CertFile)
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
func (s *Host) ControlGroup() *byterate.Group {
	g := s.RateLimiter().Group(s.Client.ControlGroup())
	s.RLock()
	g.SetMaxConns(int64(s.MaxConn))
	s.RUnlock()
	return g
}

// SetMaxConn updates the library cap without reclaiming active slots.
func (s *Host) SetMaxConn(limit int) {
	s.Lock()
	s.MaxConn = max(limit, 0)
	s.rateMu.Lock()
	if s.runtimeRate != nil {
		s.runtimeRate.Group(nil).SetMaxConns(int64(s.MaxConn))
	}
	s.rateMu.Unlock()
	s.Unlock()
}
func (s *Host) CurrentConnections() int32 { return s.RateLimits.connectionCount() }

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
	return trimArr(textutil.SplitLines(strings.ReplaceAll(targetStr, "：", ":")))
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
		log.Warn("invalid target list", "target", s.TargetStr, "err", err)
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

// Addresses returns the distinct addresses a health check can reach. A target
// the server dials itself has none: its own dials judge it.
func (s *Target) Addresses() []string {
	if s == nil || s.LocalProxy {
		return nil
	}
	s.mu.Lock()
	targetStr := s.TargetStr
	s.mu.Unlock()
	addrs := parseTargets(targetStr)
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if !strings.Contains(addr, "://") && !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}
	return out
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

// RelayTarget names where the tunnel forwards to.
func (s *Tunnel) RelayTarget() string {
	if s.Target == nil {
		return ""
	}
	return s.Target.TargetStr
}

// SetIsHTTP records whether the tunnel's traffic looks like HTTP.
func (s *Tunnel) SetIsHTTP(isHTTP bool) {
	s.Lock()
	s.IsHttp = isHTTP
	s.Unlock()
}

// Target types of a tunnel: what it forwards to its target.
const (
	TargetTCP = "tcp"
	TargetUDP = "udp"
	TargetAll = "all" // both
)

// NormalizeTargetType returns the target type t names, TargetAll for any
// value other than TargetTCP and TargetUDP.
func NormalizeTargetType(t string) string {
	if t == TargetTCP || t == TargetUDP {
		return t
	}
	return TargetAll
}

// totpSeq separates the web password from its TOTP secret.
const totpSeq = "totp:"

// trimArr trims the elements of arr and drops the empty ones.
func trimArr(arr []string) []string {
	newArr := make([]string, 0)
	for _, v := range arr {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			newArr = append(newArr, trimmed)
		}
	}
	return newArr
}

// LogRoute names t in log lines: "<mode>:<port>" for a tunnel, the domain for
// one built for a host entry.
func (t *Tunnel) LogRoute() string {
	if t == nil {
		return ""
	}
	if t.hostRoute != "" {
		return t.hostRoute
	}
	return t.Mode + ":" + strconv.Itoa(t.Port)
}

// ListenKinds reports the protocols t listens on at its port: TCP unless the
// mode is udp, and UDP for udp, socks5 and a mixed proxy with SOCKS5. A
// secret tunnel listens nowhere.
func (t *Tunnel) ListenKinds() (tcp, udp bool) {
	switch t.Mode {
	case "secret", ModeService:
		return false, false
	case "udp":
		return false, true
	case "socks5":
		return true, true
	case "mixProxy":
		return true, t.Socks5Proxy
	}
	return true, false
}

// ListenIPsOverlap reports whether two listen addresses can accept the same
// connection: equal, or either is the wildcard (empty, 0.0.0.0 or ::).
func ListenIPsOverlap(a, b string) bool {
	wild := func(s string) bool {
		ip, err := netip.ParseAddr(strings.TrimSpace(s))
		return strings.TrimSpace(s) == "" || err == nil && ip.IsUnspecified()
	}
	if wild(a) || wild(b) {
		return true
	}
	x, ex := netip.ParseAddr(strings.TrimSpace(a))
	y, ey := netip.ParseAddr(strings.TrimSpace(b))
	if ex == nil && ey == nil {
		return x.Unmap() == y.Unmap()
	}
	return strings.EqualFold(a, b)
}
