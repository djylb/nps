package file

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

func TestFlowSubDoesNotGoNegative(t *testing.T) {
	flow := &Flow{InletFlow: 5, ExportFlow: 3}

	flow.Sub(10, 8)

	if flow.InletFlow != 0 {
		t.Fatalf("expected inlet flow to clamp at 0, got %d", flow.InletFlow)
	}
	if flow.ExportFlow != 0 {
		t.Fatalf("expected export flow to clamp at 0, got %d", flow.ExportFlow)
	}
}

func TestSortClientByKey(t *testing.T) {
	clients := &sync.Map{}
	clients.Store("a", &Client{Id: 1, Flow: &Flow{ExportFlow: 10, InletFlow: 100}})
	clients.Store("b", &Client{Id: 2, Flow: &Flow{ExportFlow: 30, InletFlow: 50}})
	clients.Store("c", &Client{Id: 3, Flow: &Flow{ExportFlow: 20, InletFlow: 80}})

	desc := sortClientByKey(clients, "ExportFlow", "desc")
	if len(desc) != 3 || desc[0] != 1 || desc[1] != 3 || desc[2] != 2 {
		t.Fatalf("unexpected desc sort result: %v", desc)
	}

	asc := sortClientByKey(clients, "InletFlow", "asc")
	if len(asc) != 3 || asc[0] != 1 || asc[1] != 3 || asc[2] != 2 {
		t.Fatalf("unexpected asc sort result: %v", asc)
	}
}

func TestEnsureWebPasswordExtractsAndRepairsTotpSecret(t *testing.T) {
	validSecret := "JBSWY3DPEHPK3PXP"
	c := &Client{
		WebPassword: "plainpass" + common.TOTPSeq + validSecret,
	}

	c.EnsureWebPassword()

	if c.WebPassword != "plainpass" {
		t.Fatalf("expected password suffix to be removed, got %q", c.WebPassword)
	}
	if c.WebTotpSecret != validSecret {
		t.Fatalf("expected extracted secret %q, got %q", validSecret, c.WebTotpSecret)
	}

	c.WebTotpSecret = "invalid"
	c.EnsureWebPassword()
	if c.WebTotpSecret == "invalid" {
		t.Fatalf("expected invalid secret to be regenerated")
	}
	if !crypt.IsValidTOTPSecret(c.WebTotpSecret) {
		t.Fatalf("expected regenerated secret to be valid, got %q", c.WebTotpSecret)
	}
}

func TestTunnelCompileDestACLNormalizesAndHandlesEmptyRules(t *testing.T) {
	t.Run("invalid mode falls back to off", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: 99, DestAclRules: "Allow all"}
		tn.CompileDestACL()

		if tn.DestAclMode != AclOff {
			t.Fatalf("expected mode to fallback to off, got %d", tn.DestAclMode)
		}
		if tn.DestAclSet != nil {
			t.Fatalf("expected acl set to be nil in off mode")
		}
	})

	t.Run("whitelist with empty rules denies all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "  \r\n\t "}
		tn.CompileDestACL()

		if tn.DestAclRules != "" {
			t.Fatalf("expected normalized empty rules, got %q", tn.DestAclRules)
		}
		if tn.DestAclSet == nil {
			t.Fatalf("expected acl set to be created for empty whitelist")
		}
	})

	t.Run("blacklist with empty rules allows all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist, DestAclRules: ""}
		tn.CompileDestACL()

		if tn.DestAclSet != nil {
			t.Fatalf("expected acl set to stay nil for empty blacklist")
		}
	})
}

func TestTunnelAllowsDestination(t *testing.T) {
	t.Run("nil tunnel allows all", func(t *testing.T) {
		var tn *Tunnel
		if !tn.AllowsDestination("example.com:443") {
			t.Fatalf("expected nil tunnel to allow destination")
		}
	})

	t.Run("whitelist allows matched and blocks unmatched", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "example.com"}
		tn.CompileDestACL()

		if !tn.AllowsDestination("example.com:443") {
			t.Fatalf("expected matched destination to be allowed in whitelist mode")
		}
		if tn.AllowsDestination("blocked.com:443") {
			t.Fatalf("expected unmatched destination to be denied in whitelist mode")
		}
	})

	t.Run("blacklist denies matched and allows unmatched", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist, DestAclRules: "blocked.com"}
		tn.CompileDestACL()

		if tn.AllowsDestination("blocked.com:80") {
			t.Fatalf("expected matched destination to be denied in blacklist mode")
		}
		if !tn.AllowsDestination("safe.com:80") {
			t.Fatalf("expected unmatched destination to be allowed in blacklist mode")
		}
	})

	t.Run("on demand compile path works", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "allow.me", DestAclSet: nil}

		if !tn.AllowsDestination("allow.me:8080") {
			t.Fatalf("expected destination to be allowed after on-demand compile")
		}
		if tn.DestAclSet == nil {
			t.Fatalf("expected on-demand compile to populate acl set")
		}
	})
}

func TestTargetConcurrentRotationAndHealthUpdates(t *testing.T) {
	target := &Target{TargetStr: "a:1\r\nb:2\n\nc：3"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = target.GetRandomTarget()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				target.RemoveTarget("b:2")
				target.RestoreTarget("b:2")
			}
		}()
	}
	wg.Wait()

	seen := map[string]int{}
	for i := 0; i < 3; i++ {
		addr, err := target.GetRandomTarget()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[addr]++
	}
	if len(seen) != 3 || seen["a:1"] != 1 || seen["b:2"] != 1 || seen["c:3"] != 1 {
		t.Fatalf("unexpected rotation: %v", seen)
	}
	if target.RestoreTarget("d:4") {
		t.Fatalf("an unconfigured target must not be added")
	}
	if !target.RemoveTarget("a:1") || target.RemoveTarget("a:1") {
		t.Fatalf("unexpected RemoveTarget result")
	}
}

func TestClientGetConnHonoursMaxConn(t *testing.T) {
	c := &Client{MaxConn: 5}
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := 0
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.GetConn() {
				mu.Lock()
				got++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if got != 5 || c.NowConn != 5 {
		t.Fatalf("granted %d, NowConn %d, want 5", got, c.NowConn)
	}
	for i := 0; i < 10; i++ {
		c.CutConn()
	}
	if c.NowConn != 0 {
		t.Fatalf("NowConn = %d after over-release, want 0", c.NowConn)
	}
	c.NowConn = -3
	if !c.GetConn() || c.NowConn != 1 {
		t.Fatalf("negative count must restart at 1, got %d", c.NowConn)
	}
}

func TestConfigTaskPorts(t *testing.T) {
	cases := []struct {
		name  string
		task  *Tunnel
		ports []int
		ok    bool
	}{
		{"single tcp", &Tunnel{Mode: "tcp", Ports: "8000", Target: &Target{TargetStr: "127.0.0.1:80"}}, []int{8000}, true},
		{"multi tcp", &Tunnel{Mode: "tcp", Ports: "8000-8002", Target: &Target{TargetStr: "80,81,82"}}, []int{8000, 8001, 8002}, true},
		{"multi tcp mismatch", &Tunnel{Mode: "tcp", Ports: "8000-8002", Target: &Target{TargetStr: "80,81"}}, nil, false},
		{"multi httpProxy", &Tunnel{Mode: "httpProxy", Ports: "8000,8001", Target: &Target{}}, []int{8000, 8001}, true},
		{"secret", &Tunnel{Mode: "secret", Target: &Target{TargetStr: "127.0.0.1:22"}}, []int{0}, true},
		{"secret with port", &Tunnel{Mode: "secret", Ports: "9000", Target: &Target{TargetStr: "127.0.0.1:22"}}, []int{9000, 0}, true},
		{"file", &Tunnel{Mode: "file", Target: &Target{}}, []int{0}, true},
		{"no port", &Tunnel{Mode: "tcp", Target: &Target{TargetStr: "80"}}, nil, false},
		{"no target", &Tunnel{Mode: "tcp", Ports: "8000"}, nil, false},
	}
	for _, tc := range cases {
		ports, _, ok := ConfigTaskPorts(tc.task)
		if ok != tc.ok || !reflect.DeepEqual(ports, tc.ports) {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, ports, ok, tc.ports, tc.ok)
		}
	}
}

// TestClientJSONGolden pins the JSON form of a Client, as stored in
// clients.json and returned by the web API.
func TestClientJSONGolden(t *testing.T) {
	c := &Client{
		Cnf:        &Config{U: "u", P: "p", Compress: true},
		Id:         7,
		VerifyKey:  "vkey",
		Status:     true,
		RateLimit:  256,
		Flow:       &Flow{ExportFlow: 1, InletFlow: 2, FlowLimit: 3},
		Rate:       rate.NewRate(256 * 1024),
		MaxConn:    10,
		CreateTime: "2026-01-02 03:04:05",
	}
	const golden = `{"Cnf":{"U":"u","P":"p","Compress":true,"Crypt":false},"Id":7,"VerifyKey":"vkey","Mode":"","Addr":"","LocalAddr":"","Remark":"","Status":true,"IsConnect":false,"RateLimit":256,"Flow":{"ExportFlow":1,"InletFlow":2,"FlowLimit":3,"TimeLimit":"0001-01-01T00:00:00Z"},"ExportFlow":0,"InletFlow":0,"Rate":{"NowRate":0,"Limit":262144},"NoStore":false,"NoDisplay":false,"MaxConn":10,"NowConn":0,"WebUserName":"","WebPassword":"","WebTotpSecret":"","ConfigConnAllow":false,"MaxTunnelNum":0,"Version":"","BlackIpList":null,"CreateTime":"2026-01-02 03:04:05","LastOnlineTime":""}`
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != golden {
		t.Fatalf("Client JSON changed:\n got %s\nwant %s", b, golden)
	}

	c.Rate = nil
	b, err = json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `,"Rate":null,`) {
		t.Fatalf("nil Rate JSON: %s", b)
	}

	// Decoding ignores the stored rate: it yields an unlimited Rate.
	var back Client
	if err := json.Unmarshal([]byte(golden), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Rate == nil || back.Rate.Limit() != 0 || back.Rate.Now() != 0 {
		t.Fatalf("decoded Rate = %+v, want an unlimited Rate", back.Rate)
	}
	back.Rate.Start()
	if back.Rate.Limit() != 0 {
		t.Fatalf("decoded Rate limit after Start = %d, want 0", back.Rate.Limit())
	}

	var none Client
	if err := json.Unmarshal(b, &none); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if none.Rate != nil {
		t.Fatalf("decoded null Rate = %+v, want nil", none.Rate)
	}
}
