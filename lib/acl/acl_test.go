package acl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djylb/geomatch"
)

func TestNormalizeSource(t *testing.T) {
	got, err := NormalizeSource(ScopeUser, []string{" 1.2.3.4 ", "", "# comment", "10.1.2.3/8", "192.168.*", "1.2.3.4", "geoip:private", "[2001:db8::1]:80", "a\r\nb.c"})
	want := []string{"1.2.3.4", "10.0.0.0/8", "192.168.0.0/16", "geoip:private", "2001:db8::1"}
	if !slices.Equal(got, want) {
		t.Fatalf("rules = %q, want %q", got, want)
	}
	// "a" and "b.c" are no IP rules
	var re *RuleError
	if !errors.As(err, &re) || len(re.errs) != 2 || !errors.Is(err, geomatch.ErrKindDisabled) {
		t.Fatalf("err = %v, want the two host names rejected", err)
	}
	if !strings.Contains(err.Error(), "line 9") {
		t.Fatalf("err = %v, want line numbers", err)
	}
}

func TestNormalizeSourceScopes(t *testing.T) {
	if _, err := NormalizeSource(ScopeUser, []string{"ext-ip:/etc/passwd:cn"}); !errors.Is(err, geomatch.ErrExtDenied) {
		t.Fatalf("user ext-ip: err = %v, want ErrExtDenied", err)
	}
	if _, err := NormalizeSource(ScopeGlobal, []string{"ext-ip:/etc/passwd:cn"}); !errors.Is(err, geomatch.ErrExtDenied) {
		t.Fatalf("global ext-ip outside the geodata directory: err = %v, want ErrExtDenied", err)
	}
	many := make([]string, DefaultMaxRules+1)
	for i := range many {
		many[i] = net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).String()
	}
	if _, err := NormalizeSource(ScopeUser, many); !errors.Is(err, geomatch.ErrLimit) {
		t.Fatalf("too many user rules: err = %v, want ErrLimit", err)
	}
	if _, err := NormalizeSource(ScopeGlobal, many); err != nil {
		t.Fatalf("global rules are not limited: %v", err)
	}
	SetMaxRules(0)
	t.Cleanup(func() { SetMaxRules(DefaultMaxRules) })
	if _, err := NormalizeSource(ScopeUser, many); err != nil {
		t.Fatalf("no limit configured: %v", err)
	}
	SetMaxRules(2)
	if _, err := NormalizeSource(ScopeUser, many[:3]); !errors.Is(err, geomatch.ErrLimit) {
		t.Fatalf("configured limit: err = %v, want ErrLimit", err)
	}
}

func TestNormalizeDest(t *testing.T) {
	got, err := NormalizeDest("full:Example.com\r\nads.example\n*.sub.com\n*root.com\n10.0.0.0/8\n1.2.3.4:80\nkeyword:ads.example\n# x")
	if err != nil {
		t.Fatal(err)
	}
	// the rules mean what geomatch defines; a bare host name is a keyword
	want := "full:example.com\nkeyword:ads.example\n*.sub.com\ndomain:root.com\n10.0.0.0/8\n1.2.3.4"
	if got != want {
		t.Fatalf("rules = %q, want %q", got, want)
	}
	if again, err := NormalizeDest(got); err != nil || again != got {
		t.Fatalf("normalizing again = %q, %v", again, err)
	}
	if _, err := NormalizeDest("ext:/etc/passwd:cn"); err == nil {
		t.Fatal("ext: rule accepted in a destination list")
	}
}

func addr(s string) net.Addr {
	a, _ := net.ResolveTCPAddr("tcp", s)
	return a
}

func mustSource(t *testing.T, priority int, black, white []string) *Source {
	t.Helper()
	s, err := CompileSource(ScopeUser, priority, black, white)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourcePriorities(t *testing.T) {
	black := []string{"10.0.0.0/8"}
	white := []string{"10.1.0.0/16", "192.168.0.0/16"}
	cases := []struct {
		ip                    string
		denyFirst, allowFirst bool
	}{
		{"10.1.2.3", false, true},   // on both lists
		{"10.2.0.1", false, false},  // blacklist only
		{"192.168.1.1", true, true}, // whitelist only
		{"8.8.8.8", false, true},    // neither
	}
	df := mustSource(t, DenyFirst, black, white)
	af := mustSource(t, AllowFirst, black, white)
	for _, c := range cases {
		a := addr(c.ip + ":1234")
		if got := Allows(a, nil, nil, df); got != c.denyFirst {
			t.Errorf("deny-first %s = %v, want %v", c.ip, got, c.denyFirst)
		}
		if got := Allows(a, nil, nil, af); got != c.allowFirst {
			t.Errorf("allow-first %s = %v, want %v", c.ip, got, c.allowFirst)
		}
	}
}

func TestSourceOneList(t *testing.T) {
	onlyBlack := mustSource(t, DenyFirst, []string{"1.2.3.4"}, nil)
	onlyWhite := mustSource(t, AllowFirst, nil, []string{"1.2.3.4"})
	if Allows(addr("1.2.3.4:1"), nil, nil, onlyBlack) || !Allows(addr("5.6.7.8:1"), nil, nil, onlyBlack) {
		t.Fatal("blacklist only")
	}
	if !Allows(addr("1.2.3.4:1"), nil, nil, onlyWhite) || Allows(addr("5.6.7.8:1"), nil, nil, onlyWhite) {
		t.Fatal("whitelist only")
	}
	if s := mustSource(t, AllowFirst, nil, nil); s != nil {
		t.Fatal("empty lists compiled to a policy")
	}
}

func TestLevels(t *testing.T) {
	global := mustSource(t, DenyFirst, []string{"1.1.1.1"}, nil)
	client := mustSource(t, DenyFirst, nil, []string{"1.1.1.0/24", "2.2.2.2"})
	tunnel := mustSource(t, AllowFirst, []string{"2.2.2.2"}, []string{"1.1.1.1"})

	// the global blacklist cannot be overridden by lower whitelists
	d := Decide(addr("1.1.1.1:80"), nil, global, client, tunnel)
	if d.Allowed || d.Level != 0 || d.Rule.String() != "1.1.1.1" {
		t.Fatalf("global deny: %+v", d)
	}
	// the client whitelist decides for addresses it does not list
	if d := Decide(addr("3.3.3.3:80"), nil, global, client, tunnel); d.Allowed || d.Level != 1 {
		t.Fatalf("client deny: %+v", d)
	}
	// the tunnel blacklist applies below an allowing client
	if d := Decide(addr("2.2.2.2:80"), nil, global, client, tunnel); d.Allowed || d.Level != 2 {
		t.Fatalf("tunnel deny: %+v", d)
	}
	if !Allows(addr("1.1.1.9:80"), global, client, tunnel) {
		t.Fatal("address every level allows was denied")
	}
	if !Allows(addr("9.9.9.9:80"), nil, nil, nil) || !DecideString("9.9.9.9:80", nil, nil, nil, nil).Allowed {
		t.Fatal("no lists denied an address")
	}
	if d := DecideString("[::ffff:1.1.1.1]:80", nil, global, nil, nil); d.Allowed {
		t.Fatal("IPv4-mapped address passed the IPv4 blacklist")
	}
}

func TestGeoIPWithoutData(t *testing.T) {
	s, err := CompileSource(ScopeUser, DenyFirst, []string{"geoip:cn", "1.2.3.4"}, nil)
	if !errors.Is(err, geomatch.ErrGeoData) {
		t.Fatalf("err = %v, want the geoip: rule skipped for missing data", err)
	}
	if Allows(addr("1.2.3.4:1"), nil, nil, s) {
		t.Fatal("the other rules were not applied")
	}
	w, _ := CompileSource(ScopeUser, DenyFirst, nil, []string{"geoip:cn"})
	if Allows(addr("1.2.3.4:1"), nil, nil, w) {
		t.Fatal("a whitelist whose rules all failed allowed an address")
	}
}

func TestDestPolicy(t *testing.T) {
	d, err := CompileDest(1, "full:example.com\n*.example.org\nads")
	if err != nil {
		t.Fatal(err)
	}
	for a, want := range map[string]bool{"example.com:443": true, "a.example.com:443": false, "x.example.org": true, "example.org": false, "ads.net:80": true} {
		if got := d.Allows(a); got != want {
			t.Errorf("Allows(%q) = %v, want %v", a, got, want)
		}
	}
	if d, _ := CompileDest(0, "example.com"); d != nil || !d.Allows("x:1") {
		t.Fatal("off mode compiled a list")
	}
}

func TestRejectedRulesSayWhy(t *testing.T) {
	if _, err := NormalizeDest("ful:example.com"); !errors.Is(err, geomatch.ErrInvalidRule) {
		t.Fatalf("misspelt kind: err = %v, want it rejected", err)
	}
	_, err := NormalizeSource(ScopeUser, []string{"1.1.1.1-1.1.1.5"})
	if !errors.Is(err, geomatch.ErrInvalidRule) {
		t.Fatalf("IP range: err = %v, want it rejected", err)
	}
	_, err = NormalizeSource(ScopeUser, []string{"example.com"})
	var ke *geomatch.KindError
	if !errors.As(err, &ke) || !strings.Contains(err.Error(), "allowed") {
		t.Fatalf("host name in a source list: err = %v, want the allowed kinds named", err)
	}
}

func TestGeoGenerationGrowsWhenAFileChanges(t *testing.T) {
	old := geo.Files()
	t.Cleanup(func() { geo.SetFiles(old) })
	path := filepath.Join(t.TempDir(), "geoip.dat")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	SetGeoData(path, "")
	_, _ = CompileSource(ScopeUser, DenyFirst, []string{"geoip:cn"}, nil)
	before := GeoGeneration()
	if GeoGeneration() != before {
		t.Fatal("generation grew without a change")
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(path, future, future)
	if GeoGeneration() == before {
		t.Fatal("generation did not grow after the geoip file changed")
	}
}

func TestDecideNamesLevel(t *testing.T) {
	client, err := CompileSource(ScopeUser, DenyFirst, []string{"192.0.2.0/24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := DecideString("192.0.2.1:80", []string{"global", "client 3", "tunnel 5"}, nil, client, nil)
	if d.Allowed || d.Name != "client 3" || !strings.Contains(d.String(), "(client 3)") {
		t.Fatalf("decision = %+v (%s), want denied by client 3", d, d)
	}
}

// Older versions stored the lines entered on a page as one rule.
func TestCompileSourceSplitsStoredLines(t *testing.T) {
	s, err := CompileSource(ScopeUser, DenyFirst, []string{"203.0.113.9\n\n10.9.9.9\r\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"203.0.113.9:1", "10.9.9.9:1"} {
		if AllowsString(addr, nil, s, nil) {
			t.Errorf("%s allowed by a stored multi-line blacklist", addr)
		}
	}
	if !AllowsString("198.51.100.1:1", nil, s, nil) {
		t.Error("address off the list denied")
	}
	if got, _ := NormalizeSource(ScopeUser, []string{"203.0.113.9\n10.9.9.9"}); len(got) != 2 {
		t.Errorf("NormalizeSource = %q, want two rules", got)
	}
}
