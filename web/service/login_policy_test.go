package service

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestDefaultLoginPolicyUsesConfigProvider(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginBanTime:      7,
				LoginIPBanTime:    11,
				LoginUserBanTime:  13,
				LoginMaxFailTimes: 5,
				LoginMaxBody:      2048,
				LoginMaxSkew:      9000,
			},
		}
	})

	settings := policy.Settings()
	if settings.BanTime != 7 || settings.IPBanTime != 11 || settings.UserBanTime != 13 {
		t.Fatalf("Settings() returned unexpected ban settings: %+v", settings)
	}
	if settings.MaxFailTimes != 5 || settings.MaxLoginBody != 2048 || settings.MaxSkew != 9000 {
		t.Fatalf("Settings() returned unexpected limits: %+v", settings)
	}
}

func TestDefaultLoginPolicyBanLifecycle(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginBanTime:      60,
				LoginIPBanTime:    60,
				LoginUserBanTime:  60,
				LoginMaxFailTimes: 3,
			},
		}
	})

	policy.RecordIPFailure("127.0.0.1")
	if !policy.IsIPBanned("127.0.0.1") {
		t.Fatal("IsIPBanned() = false, want true after a failure")
	}

	records := policy.BanList()
	if len(records) != 1 || records[0].BanType != "ip" || !records[0].IsBanned || records[0].FailTimes != 1 || records[0].Key != "127.0.0.1" {
		t.Fatalf("BanList() = %+v, want one banned ip record", records)
	}

	if !policy.RemoveBan("127.0.0.1") {
		t.Fatal("RemoveBan() = false, want true")
	}
	if policy.IsIPBanned("127.0.0.1") {
		t.Fatal("IsIPBanned() = true after RemoveBan(), want false")
	}
	if len(policy.BanList()) != 0 {
		t.Fatalf("BanList() = %+v after RemoveBan(), want none", policy.BanList())
	}
}

func TestDefaultLoginPolicyCountsFailures(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginIPBanTime:    60,
				LoginUserBanTime:  60,
				LoginMaxFailTimes: 3,
			},
		}
	})
	for range 2 {
		policy.RecordUserFailure("alice", "192.0.2.9")
		policy.RecordIPFailure("2001:db8::1")
	}
	if policy.IsUserBanned("alice") || policy.IsIPBanned("2001:db8::2") {
		t.Fatal("banned before login_max_fail_times failures")
	}
	policy.ClearUserFailures("alice")
	policy.RecordUserFailure("alice", "192.0.2.9")
	policy.RecordIPFailure("2001:db8::ffff")
	if policy.IsUserBanned("alice") {
		t.Fatal("IsUserBanned() = true, want the failures cleared by a login")
	}
	// Addresses of one /64 count together.
	if !policy.IsIPBanned("2001:db8::3") {
		t.Fatal("IsIPBanned() = false, want the /64 banned")
	}
	var ip, user *LoginBanRecord
	for _, r := range policy.BanList() {
		switch r.BanType {
		case "ip":
			ip = &r
		case "username":
			user = &r
		}
	}
	if ip == nil || ip.Key != "2001:db8::/64" || !ip.IsBanned || ip.FailTimes != 3 {
		t.Fatalf("ip record = %+v", ip)
	}
	if user == nil || user.Key != "alice" || user.IsBanned || user.FailTimes != 1 {
		t.Fatalf("user record = %+v", user)
	}
	if !policy.RemoveBan("2001:db8::/64") || policy.IsIPBanned("2001:db8::1") {
		t.Fatal("RemoveBan(prefix) did not lift the ban")
	}
}

func TestDefaultLoginPolicyKeepsBansAcrossSettings(t *testing.T) {
	cfg := &servercfg.Snapshot{Security: servercfg.SecurityConfig{LoginBanTime: 60, LoginIPBanTime: 60, LoginMaxFailTimes: 3}}
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot { return cfg })
	policy.RecordIPFailure("192.0.2.1")
	cfg = &servercfg.Snapshot{Security: servercfg.SecurityConfig{LoginBanTime: 30, LoginIPBanTime: 60, LoginMaxFailTimes: 5}}
	if !policy.IsIPBanned("192.0.2.1") {
		t.Fatal("ban lost when the settings changed")
	}
	if policy.IsIPBanned("not an address") {
		t.Fatal("an invalid address is banned")
	}
}

func TestLoginACL(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot { return cfg })
	tests := []struct {
		mode    int
		rules   string
		ip      string
		allowed bool
	}{
		{0, "192.0.2.1", "192.0.2.1", true},
		{1, "192.0.2.1,198.51.100.0/24", "198.51.100.7", true},
		{1, "192.0.2.1,198.51.100.0/24", "203.0.113.1", false},
		{2, "192.0.2.1\n198.51.100.0/24", "192.0.2.1", false},
		{2, "192.0.2.1\n198.51.100.0/24", "203.0.113.1", true},
		{1, "", "203.0.113.1", true},
	}
	for _, tt := range tests {
		cfg = &servercfg.Snapshot{Security: servercfg.SecurityConfig{LoginACLMode: tt.mode, LoginACLRules: tt.rules}}
		if got := policy.AllowsIP(tt.ip); got != tt.allowed {
			t.Errorf("AllowsIP(%s) mode %d rules %q = %v", tt.ip, tt.mode, tt.rules, got)
		}
	}
}
