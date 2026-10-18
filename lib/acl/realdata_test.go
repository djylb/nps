package acl

import (
	"os"
	"testing"
)

// TestRealGeoIP checks geoip: rules against the geoip.dat named by
// NPS_TEST_GEOIP.
func TestRealGeoIP(t *testing.T) {
	path := os.Getenv("NPS_TEST_GEOIP")
	if path == "" {
		t.Skip("NPS_TEST_GEOIP not set")
	}
	old := geo.Files()
	SetGeoData(path, "")
	t.Cleanup(func() { geo.SetFiles(old) })

	s, err := CompileSource(ScopeUser, DenyFirst, []string{"geoip:cn"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if Allows(addr("223.5.5.5:53"), nil, nil, s) {
		t.Fatal("geoip:cn blacklist allowed 223.5.5.5")
	}
	if !Allows(addr("8.8.8.8:53"), nil, nil, s) {
		t.Fatal("geoip:cn blacklist denied 8.8.8.8")
	}
	neg, err := CompileSource(ScopeUser, DenyFirst, nil, []string{"geoip:!cn"})
	if err != nil {
		t.Fatal(err)
	}
	if Allows(addr("223.5.5.5:53"), nil, nil, neg) || !Allows(addr("8.8.8.8:53"), nil, nil, neg) {
		t.Fatal("geoip:!cn whitelist")
	}
}
