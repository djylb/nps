package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

func TestEnsureWebPasswordExtractsAndRepairsTotpSecret(t *testing.T) {
	validSecret := "JBSWY3DPEHPK3PXP"
	c := &Client{
		WebPassword: "plainpass" + totpSeq + validSecret,
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

// pickTarget returns the address Dial tries first.
func pickTarget(target *Target) (string, error) {
	var picked string
	err := target.Dial(func(addr string) (bool, error) {
		picked = addr
		return false, nil
	})
	return picked, err
}

func TestTargetDialRetriesAndEjects(t *testing.T) {
	target := &Target{TargetStr: "a:1\nb:2\nc:3"}
	errDown := errors.New("refused")
	var tried []string
	err := target.Dial(func(addr string) (bool, error) {
		tried = append(tried, addr)
		return true, errDown
	})
	if err != errDown || len(tried) != 3 {
		t.Fatalf("Dial = %v after %v, want every target tried once", err, tried)
	}

	// a failure on the way to the target is not retried
	tried = nil
	if err := target.Dial(func(addr string) (bool, error) {
		tried = append(tried, addr)
		return false, errDown
	}); err != errDown || len(tried) != 1 {
		t.Fatalf("Dial = %v after %v, want one attempt", err, tried)
	}

	// a:1 keeps failing and is ejected; the others take its connections
	for i := 0; i < 2*targetOutlier.ConsecutiveFailures; i++ {
		_ = target.Dial(func(addr string) (bool, error) {
			if addr == "a:1" {
				return true, errDown
			}
			return true, nil
		})
	}
	for range 6 {
		if addr, _ := pickTarget(target); addr == "a:1" {
			t.Fatalf("ejected target picked")
		}
	}
	// the client's checks clear the ejection once it reports a:1 healthy again
	target.SetHealthy("a:1", false)
	target.SetHealthy("a:1", true)
	seen := map[string]bool{}
	for range 3 {
		addr, _ := pickTarget(target)
		seen[addr] = true
	}
	if !seen["a:1"] {
		t.Fatalf("target not restored: %v", seen)
	}

	empty := &Target{}
	if _, err := pickTarget(empty); err != ErrNoTarget {
		t.Fatalf("empty target list: %v", err)
	}
}

func TestTargetConcurrentRotationAndHealthUpdates(t *testing.T) {
	target := &Target{TargetStr: "a:1\r\nb:2\n\nc：3"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				_, _ = pickTarget(target)
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				target.SetHealthy("b:2", false)
				target.SetHealthy("b:2", true)
			}
		}()
	}
	wg.Wait()
	// The pool may hand the last rebuild of its view to a goroutine of its
	// own, so the final health shows shortly after the updates return, and
	// the views below are awaited the same way.
	awaitAvailable(t, target, 3)

	pick := func(n int) map[string]int {
		seen := map[string]int{}
		for range n {
			addr, err := pickTarget(target)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			seen[addr]++
		}
		return seen
	}
	// A rebuild still under way may publish an equal view that starts the
	// rotation over, so only check that every address is in it.
	if seen := pick(30); len(seen) != 3 {
		t.Fatalf("unexpected rotation: %v", seen)
	}
	if target.SetHealthy("d:4", true) {
		t.Fatalf("an unconfigured target must not be added")
	}
	if !target.SetHealthy("a:1", false) {
		t.Fatalf("a configured target must be found")
	}
	awaitAvailable(t, target, 2)
	if seen := pick(4); seen["a:1"] != 0 || seen["b:2"] != 2 || seen["c:3"] != 2 {
		t.Fatalf("unexpected rotation without a:1: %v", seen)
	}
	target.SetHealthy("b:2", false)
	target.SetHealthy("c:3", false)
	awaitAvailable(t, target, 0)
	if _, err := pickTarget(target); err == nil {
		t.Fatalf("expected an error with every target down")
	}

	// a changed list keeps the health of the addresses it keeps, and an
	// address listed twice gets twice the share
	target.TargetStr = "c:3\nd:4\nd:4"
	awaitAvailable(t, target, 1)
	if seen := pick(4); seen["c:3"] != 0 || seen["d:4"] != 4 {
		t.Fatalf("unexpected rotation after change: %v", seen)
	}
	target.SetHealthy("c:3", true)
	awaitAvailable(t, target, 2)
	if seen := pick(3); seen["c:3"] != 1 || seen["d:4"] != 2 {
		t.Fatalf("unexpected weighted rotation: %v", seen)
	}
}

func TestClientGetConnHonoursMaxConn(t *testing.T) {
	c := &Client{MaxConn: 5}
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := 0
	for range 64 {
		wg.Go(func() {
			if c.GetConn() {
				mu.Lock()
				got++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if got != 5 || c.CurrentConnections() != 5 {
		t.Fatalf("granted %d, NowConn %d, want 5", got, c.CurrentConnections())
	}
	for range 5 {
		c.CutConn()
	}
	if c.CurrentConnections() != 0 {
		t.Fatalf("NowConn = %d after over-release, want 0", c.CurrentConnections())
	}
	if !c.GetConn() || c.CurrentConnections() != 1 {
		t.Fatal("released slot not reusable")
	}
	c.CutConn()
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
		{"file without port", &Tunnel{Mode: "file", Target: &Target{}}, nil, false},
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
	if got := client.Budget().Remaining(); got != 324<<10+1 {
		t.Fatalf("client Remaining() = %d, want %d", got, 324<<10+1)
	}
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
	if v := client.Values(); v.Used != 1400<<10 {
		t.Fatalf("client Used = %d, want %d", v.Used, 1400<<10)
	}
	SetFlowLimitActual(false)
	if traffic, _ := client.Over(); traffic {
		t.Fatal("the factor change back not followed")
	}
}

func TestClientFlowTotalsRaceFree(t *testing.T) {
	c := NewClient("vk", false, false)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range int64(1000) {
			c.InletFlow.Store(i)
			c.ExportFlow.Store(i)
		}
	}()
	for range 100 {
		if _, err := json.Marshal(c); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	b, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(b), `"ExportFlow":999,"InletFlow":999`) {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var back Client
	if err := json.Unmarshal(b, &back); err != nil || back.InletFlow.Load() != 999 {
		t.Fatalf("unmarshal inlet = %d, %v", back.InletFlow.Load(), err)
	}
}

func TestUpdateRacesWithClone(t *testing.T) {
	task := &Tunnel{Flow: &Flow{}, Target: &Target{}, MultiAccount: &MultiAccount{}, Client: &Client{Id: 1}}
	other := &Tunnel{Remark: "x", Target: &Target{}, MultiAccount: &MultiAccount{}}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			task.Update(other)
		}
	})
	wg.Go(func() {
		for range 100 {
			_ = task.Clone()
		}
	})
	wg.Wait()
}

// awaitAvailable waits until the routing view of target has n addresses
// available.
func awaitAvailable(t *testing.T, target *Target, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for target.targetPool().Available() != n {
		if time.Now().After(deadline) {
			t.Fatalf("available = %d, want %d", target.targetPool().Available(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNormalizeTargetType(t *testing.T) {
	for in, want := range map[string]string{"tcp": TargetTCP, "udp": TargetUDP, "all": TargetAll, "": TargetAll, "TCP": TargetAll, "both": TargetAll} {
		if got := NormalizeTargetType(in); got != want {
			t.Errorf("NormalizeTargetType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCompress(t *testing.T) {
	for in, want := range map[string]string{"": "", "snappy": "snappy", " ZSTD ": "zstd", "flate": "flate", "off": "off", "none": "", "gzip": ""} {
		if got := NormalizeCompress(in); got != want {
			t.Errorf("NormalizeCompress(%q) = %q, want %q", in, got, want)
		}
	}
	if NormalizeClientCompress("off") != "" || NormalizeClientCompress("zstd") != "zstd" {
		t.Error("NormalizeClientCompress")
	}
	// the override reaches the stream the way ResolveCompress reads it
	if got := ResolveCompress(&Client{Cnf: &Config{Compress: true}}, NormalizeCompress("flate")); len(got) != 1 || got[0] != "flate" {
		t.Errorf("ResolveCompress = %v", got)
	}
	if got := ResolveCompress(&Client{Cnf: &Config{Compress: true}}, NormalizeCompress("off")); got != nil {
		t.Errorf("off = %v", got)
	}
}

func TestParseClientCompress(t *testing.T) {
	for _, c := range []struct {
		compress, alg string
		on            bool
		want          string
	}{
		{"", "", false, ""},
		{"0", "", false, ""},
		{"0", "zstd", false, ""},
		{"off", "", false, ""},
		{"1", "", true, ""},
		{"true", "", true, ""},
		{"1", "zstd", true, "zstd"},
		{"1", "OFF", false, ""},
		{"1", "bogus", true, ""},
		{"snappy", "", true, "snappy"},
		{"zstd", "flate", true, "zstd"},
		{" Flate ", "", true, "flate"},
	} {
		on, alg := ParseClientCompress(c.compress, c.alg)
		if on != c.on || alg != c.want {
			t.Errorf("ParseClientCompress(%q, %q) = %v, %q; want %v, %q", c.compress, c.alg, on, alg, c.on, c.want)
		}
	}
}

func TestMatchTargetPorts(t *testing.T) {
	ports := []int{8000, 8001, 8002}
	if got, ok := MatchTargetPorts(ports, []int{1, 2, 3}, false); !ok || got[2] != 3 {
		t.Errorf("one to one = %v %v", got, ok)
	}
	if _, ok := MatchTargetPorts(ports, []int{80}, false); ok {
		t.Error("a single target must not match without single")
	}
	if got, ok := MatchTargetPorts(ports, []int{80}, true); !ok || len(got) != 3 || got[0] != 80 || got[2] != 80 {
		t.Errorf("single = %v %v", got, ok)
	}
	if _, ok := MatchTargetPorts(ports, []int{1, 2}, true); ok {
		t.Error("two targets for three ports must not match")
	}
}
