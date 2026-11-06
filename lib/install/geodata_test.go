package install

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncGeoData(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "conf")
	writeFile(t, filepath.Join(pkg, "geoip.dat"), "ip-2")
	writeFile(t, filepath.Join(pkg, "geosite.dat"), "site-2")
	writeFile(t, filepath.Join(pkg, geoVersionFile), "202602\n")

	conf := filepath.Join(t.TempDir(), "conf")
	ip, site := filepath.Join(conf, "geoip.dat"), filepath.Join(conf, "geosite.dat")

	// missing files are copied
	if err := SyncGeoData(pkg, ip, site); err != nil {
		t.Fatal(err)
	}
	if readFile(t, ip) != "ip-2" || readFile(t, site) != "site-2" || GeoDataVersion(ip) != "202602" {
		t.Fatal("missing geodata not copied")
	}

	// newer local data from update-geo is kept
	writeFile(t, ip, "ip-3")
	writeFile(t, site, "site-3")
	writeFile(t, filepath.Join(conf, geoVersionFile), "202603\n")
	if err := SyncGeoData(pkg, ip, site); err != nil {
		t.Fatal(err)
	}
	if readFile(t, ip) != "ip-3" || GeoDataVersion(ip) != "202603" {
		t.Fatal("newer local geodata replaced by an older package")
	}

	// older local data is replaced
	writeFile(t, filepath.Join(conf, geoVersionFile), "202601\n")
	if err := SyncGeoData(pkg, ip, site); err != nil {
		t.Fatal(err)
	}
	if readFile(t, ip) != "ip-2" || readFile(t, site) != "site-2" || GeoDataVersion(ip) != "202602" {
		t.Fatal("older local geodata kept")
	}

	// local data of unknown version is replaced by a versioned package
	_ = os.Remove(filepath.Join(conf, geoVersionFile))
	writeFile(t, ip, "ip-x")
	if err := SyncGeoData(pkg, ip, site); err != nil {
		t.Fatal(err)
	}
	if readFile(t, ip) != "ip-2" {
		t.Fatal("unversioned local geodata kept")
	}

	// a package without geodata changes nothing
	if err := SyncGeoData(filepath.Join(t.TempDir(), "conf"), ip, site); err != nil || readFile(t, ip) != "ip-2" {
		t.Fatalf("package without geodata: %v", err)
	}
}
