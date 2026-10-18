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

func TestTunnelCompileDestACL(t *testing.T) {
	t.Run("invalid mode falls back to off", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: 99, DestAclRules: "example.com"}
		tn.CompileDestACL()
		if tn.DestAclMode != AclOff || tn.destAcl != nil {
			t.Fatalf("mode %d, compiled %v; want off and nothing compiled", tn.DestAclMode, tn.destAcl)
		}
		if !tn.AllowsDestination("any.com:80") {
			t.Fatal("off mode denied a destination")
		}
	})

	t.Run("whitelist with empty rules denies all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "  \r\n\t "}
		tn.CompileDestACL()
		if tn.AllowsDestination("example.com:443") {
			t.Fatal("empty whitelist allowed a destination")
		}
	})

	t.Run("blacklist with empty rules allows all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist}
		tn.CompileDestACL()
		if !tn.AllowsDestination("example.com:443") {
			t.Fatal("empty blacklist denied a destination")
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

	t.Run("full: matches exactly, a bare host name is a keyword", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "full:example.com\nads"}
		tn.CompileDestACL()
		for addr, want := range map[string]bool{"example.com:443": true, "Example.COM": true, "www.example.com:443": false, "ads.example.net:443": true} {
			if got := tn.AllowsDestination(addr); got != want {
				t.Errorf("AllowsDestination(%q) = %v, want %v", addr, got, want)
			}
		}
	})

	t.Run("wildcards", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist, DestAclRules: "*.sub.com\n*root.com\n10.0.0.0/8\n1.2.3.4"}
		tn.CompileDestACL()
		for addr, want := range map[string]bool{
			"a.sub.com:80": false, "sub.com:80": true,
			"root.com:80": false, "a.root.com:80": false, "xroot.com:80": true,
			"10.1.2.3:80": false, "1.2.3.4:443": false, "8.8.8.8:53": true,
		} {
			if got := tn.AllowsDestination(addr); got != want {
				t.Errorf("AllowsDestination(%q) = %v, want %v", addr, got, want)
			}
		}
	})

	t.Run("on demand compile path works", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "allow.me"}
		if !tn.AllowsDestination("allow.me:8080") {
			t.Fatalf("expected destination to be allowed after on-demand compile")
		}
		if tn.destAcl == nil {
			t.Fatalf("expected on-demand compile to populate the compiled list")
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
	const golden = `{"Cnf":{"U":"u","P":"p","Compress":true,"Crypt":false},"Id":7,"VerifyKey":"vkey","Mode":"","Addr":"","LocalAddr":"","Remark":"","Status":true,"IsConnect":false,"RateLimit":256,"Flow":{"ExportFlow":1,"InletFlow":2,"FlowLimit":3,"TimeLimit":"0001-01-01T00:00:00Z"},"ExportFlow":0,"InletFlow":0,"Rate":{"NowRate":0,"Limit":262144},"NoStore":false,"NoDisplay":false,"MaxConn":10,"NowConn":0,"WebUserName":"","WebPassword":"","WebTotpSecret":"","ConfigConnAllow":false,"MaxTunnelNum":0,"Version":"","IpListPriority":0,"BlackIpList":null,"WhiteIpList":null,"RegisteredOnly":false,"CreateTime":"2026-01-02 03:04:05","LastOnlineTime":"","RegisterKey":"","RegisterMaxHours":0}`
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

func TestFlowLimitActual(t *testing.T) {
	t.Cleanup(func() { SetFlowLimitActual(false) })
	client := &Flow{FlowLimit: 1, InletFlow: 300 << 10, ExportFlow: 400 << 10}
	client.MarkClient()
	tunnel := &Flow{FlowLimit: 1, InletFlow: 300 << 10, ExportFlow: 400 << 10}
	if traffic, _ := client.Over(); traffic {
		t.Fatal("700 KiB of traffic over a 1 MiB client limit on the in/out traffic")
	}
	SetFlowLimitActual(true)
	if traffic, _ := client.Over(); !traffic {
		t.Fatal("1400 KiB of actual usage not over a 1 MiB client limit on the actual usage")
	}
	if traffic, _ := tunnel.Over(); traffic {
		t.Fatal("a tunnel limit counted the actual usage")
	}
}

func TestClientFlowsMarked(t *testing.T) {
	t.Cleanup(func() { SetFlowLimitActual(false) })
	SetFlowLimitActual(true)
	db := &DbUtils{JsonDb: NewJsonDb(t.TempDir())}
	c := &Client{Id: 3, VerifyKey: "vk-marked", Cnf: new(Config), Flow: &Flow{FlowLimit: 1, InletFlow: 600 << 10}}
	if err := db.NewClient(c); err != nil && !strings.Contains(err.Error(), "store") {
		t.Fatal(err)
	}
	if traffic, _ := c.Flow.Over(); !traffic {
		t.Fatal("a new client's flow not marked")
	}
	u := &Client{Id: 3, VerifyKey: "vk-marked", Cnf: new(Config), Flow: &Flow{FlowLimit: 1, InletFlow: 600 << 10}}
	_ = db.UpdateClient(u)
	if traffic, _ := u.Flow.Over(); !traffic {
		t.Fatal("an updated client's flow not marked")
	}
}
