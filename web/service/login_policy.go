package service

import (
	"net/netip"
	"sync"
	"time"

	"github.com/djylb/guard/failban"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
)

type LoginPolicySettings struct {
	BanTime      int64
	IPBanTime    int64
	UserBanTime  int64
	MaxFailTimes int
	MaxLoginBody int64
	MaxSkew      int64
}

func (s LoginPolicySettings) LoginDelayMillis() int64 {
	return s.BanTime * 1000
}

type LoginBanRecord struct {
	Key           string `json:"key"`
	FailTimes     int    `json:"fail_times"`
	LastLoginTime string `json:"last_login_time"`
	IsBanned      bool   `json:"is_banned"`
	BanType       string `json:"ban_type"`
}

// LoginPolicyService counts failed logins per client address and per user
// name and bans those that fail too often.
type LoginPolicyService interface {
	Settings() LoginPolicySettings
	IsIPBanned(ip string) bool
	IsUserBanned(username string) bool
	RecordIPFailure(ip string)
	RecordUserFailure(username string)
	// ClearUserFailures forgets the failures of a user who logged in.
	ClearUserFailures(username string)
	RemoveBan(key string) bool
	RemoveAllBans()
	// Clean frees the memory of expired failures and bans.
	Clean()
	BanList() []LoginBanRecord
}

// loginIPPrefix groups IPv6 clients by /64, the block a single host may
// hold.
var loginIPPrefix = failban.PrefixLen{V4: 32, V6: 64}

// DefaultLoginPolicy bans a key for login_ban_time seconds after each
// failure, and for login_ip_ban_time or login_user_ban_time seconds once it
// failed login_max_fail_times times within that period.
type DefaultLoginPolicy struct {
	ConfigProvider func() *servercfg.Snapshot

	mu      sync.Mutex
	current *loginLimiters
}

var sharedLoginPolicy = NewDefaultLoginPolicy(servercfg.Current)

func NewDefaultLoginPolicy(provider func() *servercfg.Snapshot) *DefaultLoginPolicy {
	return &DefaultLoginPolicy{ConfigProvider: provider}
}

func SharedLoginPolicy() *DefaultLoginPolicy {
	return sharedLoginPolicy
}

func (s *DefaultLoginPolicy) Settings() LoginPolicySettings {
	cfg := s.config()
	return LoginPolicySettings{
		BanTime:      cfg.Security.LoginBanTime,
		IPBanTime:    cfg.Security.LoginIPBanTime,
		UserBanTime:  cfg.Security.LoginUserBanTime,
		MaxFailTimes: cfg.Security.LoginMaxFailTimes,
		MaxLoginBody: cfg.Security.LoginMaxBody,
		MaxSkew:      cfg.Security.LoginMaxSkew,
	}
}

func (s *DefaultLoginPolicy) IsIPBanned(ip string) bool {
	prefix, ok := loginPrefix(ip)
	if !ok || !s.limiters().ip.banned(prefix) {
		return false
	}
	logs.Warn("%s has failed to log in too often, login blocked", ip)
	return true
}

func (s *DefaultLoginPolicy) IsUserBanned(username string) bool {
	if username == "" || !s.limiters().user.banned(username) {
		return false
	}
	logs.Warn("%s has failed to log in too often, login blocked", username)
	return true
}

func (s *DefaultLoginPolicy) RecordIPFailure(ip string) {
	if prefix, ok := loginPrefix(ip); ok {
		s.limiters().ip.fail(prefix)
	}
}

func (s *DefaultLoginPolicy) RecordUserFailure(username string) {
	if username != "" {
		s.limiters().user.fail(username)
	}
}

func (s *DefaultLoginPolicy) ClearUserFailures(username string) {
	if username != "" {
		s.limiters().user.succeed(username)
	}
}

// RemoveBan lifts the ban and forgets the failures of an address, an IPv6
// prefix or a user name.
func (s *DefaultLoginPolicy) RemoveBan(key string) bool {
	if key == "" {
		return false
	}
	l := s.limiters()
	if prefix, ok := loginPrefix(key); ok {
		return l.ip.unban(prefix)
	}
	return l.user.unban(key)
}

func (s *DefaultLoginPolicy) RemoveAllBans() {
	l := s.limiters()
	l.ip.reset()
	l.user.reset()
}

func (s *DefaultLoginPolicy) Clean() {
	l := s.limiters()
	l.ip.sweep()
	l.user.sweep()
}

func (s *DefaultLoginPolicy) BanList() []LoginBanRecord {
	l := s.limiters()
	return append(l.ip.records("ip", formatLoginPrefix), l.user.records("username", func(name string) string { return name })...)
}

func (s *DefaultLoginPolicy) config() *servercfg.Snapshot {
	if s != nil && s.ConfigProvider != nil {
		if cfg := s.ConfigProvider(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}

// limiters returns the limiters of the current settings. When the settings
// change, new limiters take over the bans in force.
func (s *DefaultLoginPolicy) limiters() *loginLimiters {
	settings := s.Settings()
	settings.MaxLoginBody, settings.MaxSkew = 0, 0
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && s.current.settings == settings {
		return s.current
	}
	next := &loginLimiters{
		settings: settings,
		ip:       newLoginLimiter[netip.Prefix](settings.BanTime, settings.IPBanTime, settings.MaxFailTimes, failban.EvictSoonest),
		user:     newLoginLimiter[string](settings.BanTime, settings.UserBanTime, settings.MaxFailTimes, failban.LockShard),
	}
	if s.current != nil {
		next.ip.restore(s.current.ip)
		next.user.restore(s.current.user)
	}
	s.current = next
	return next
}

type loginLimiters struct {
	settings LoginPolicySettings
	ip       *loginLimiter[netip.Prefix]
	user     *loginLimiter[string]
}

// loginLimiter combines a short ban after each failure (delay) with a long
// one after too many failures (count). A nil limiter is disabled.
type loginLimiter[K comparable] struct {
	delay, count *failban.Limiter[K]
	delayTime    time.Duration
	countTime    time.Duration
	threshold    int
}

func newLoginLimiter[K comparable](delaySeconds, banSeconds int64, maxFail int, full failban.FullPolicy) *loginLimiter[K] {
	l := &loginLimiter[K]{threshold: max(maxFail, 1)}
	var err error
	if delaySeconds > 0 {
		l.delayTime = time.Duration(delaySeconds) * time.Second
		if l.delay, err = failban.New[K](failban.Config{Threshold: 1, Window: l.delayTime, BanDuration: l.delayTime, Full: full}); err != nil {
			logs.Error("Login delay of %d seconds is disabled: %v", delaySeconds, err)
		}
	}
	if banSeconds > 0 {
		l.countTime = time.Duration(banSeconds) * time.Second
		if l.count, err = failban.New[K](failban.Config{Threshold: l.threshold, Window: l.countTime, BanDuration: l.countTime, Full: full}); err != nil {
			logs.Error("Login ban of %d seconds is disabled: %v", banSeconds, err)
		}
	}
	return l
}

func (l *loginLimiter[K]) each(f func(*failban.Limiter[K])) {
	if l.delay != nil {
		f(l.delay)
	}
	if l.count != nil {
		f(l.count)
	}
}

func (l *loginLimiter[K]) banned(key K) (banned bool) {
	l.each(func(f *failban.Limiter[K]) { banned = banned || f.IsBanned(key) })
	return banned
}

func (l *loginLimiter[K]) fail(key K) {
	l.each(func(f *failban.Limiter[K]) { f.Fail(key) })
}

func (l *loginLimiter[K]) succeed(key K) {
	l.each(func(f *failban.Limiter[K]) { f.Succeed(key) })
}

func (l *loginLimiter[K]) unban(key K) (found bool) {
	l.each(func(f *failban.Limiter[K]) { found = f.Unban(key) || found })
	return found
}

func (l *loginLimiter[K]) reset() {
	l.each(func(f *failban.Limiter[K]) { f.Reset() })
}

func (l *loginLimiter[K]) sweep() {
	l.each(func(f *failban.Limiter[K]) { f.Sweep() })
}

// restore takes over the bans in force of old.
func (l *loginLimiter[K]) restore(old *loginLimiter[K]) {
	move := func(from, to *failban.Limiter[K]) {
		if from == nil || to == nil {
			return
		}
		for _, b := range from.Bans() {
			_ = to.Ban(b)
		}
	}
	move(old.delay, l.delay)
	move(old.count, l.count)
}

// records lists the keys with failures or bans in force.
func (l *loginLimiter[K]) records(banType string, name func(K) string) []LoginBanRecord {
	now := time.Now()
	var list []LoginBanRecord
	index := make(map[K]int)
	add := func(key K, failures int, last time.Time, banned bool) {
		if i, ok := index[key]; ok {
			list[i].IsBanned = list[i].IsBanned || banned
			return
		}
		index[key] = len(list)
		list = append(list, LoginBanRecord{
			Key:           name(key),
			FailTimes:     failures,
			LastLoginTime: last.Format(time.DateTime),
			IsBanned:      banned,
			BanType:       banType,
		})
	}
	if l.count != nil {
		for _, b := range l.count.Bans() {
			if b.Until.After(now) {
				add(b.Key, l.threshold, b.Until.Add(-l.countTime), true)
			}
		}
		for _, p := range l.count.Pending() {
			add(p.Key, p.Failures, p.Last, l.delay != nil && l.delay.IsBanned(p.Key))
		}
	}
	if l.delay != nil {
		for _, b := range l.delay.Bans() {
			if b.Until.After(now) {
				add(b.Key, 1, b.Until.Add(-l.delayTime), true)
			}
		}
	}
	return list
}

// loginPrefix returns the key of a client address, or of a prefix that
// BanList shows.
func loginPrefix(ip string) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(ip); err == nil {
		key := loginIPPrefix.Of(prefix.Addr())
		return key, key.Bits() == prefix.Bits()
	}
	key, err := loginIPPrefix.OfRemote(ip)
	return key, err == nil
}

// formatLoginPrefix shows an IPv4 key as its address and an IPv6 key as
// its prefix.
func formatLoginPrefix(prefix netip.Prefix) string {
	if prefix.Addr().Is4() {
		return prefix.Addr().String()
	}
	return prefix.String()
}
