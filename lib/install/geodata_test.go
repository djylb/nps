package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickGeoFiles(t *testing.T) {
	dir := t.TempDir()
	files := GeoFiles(filepath.Join(dir, "geoip.dat"), filepath.Join(dir, "geosite.dat"))
	names := func(name string) string {
		t.Helper()
		got, err := PickGeoFiles(files, name)
		if err != nil {
			t.Fatal(err)
		}
		var s string
		for _, f := range got {
			s += f.Name + " "
		}
		return s
	}
	if got := names(""); got != "geoip.dat " {
		t.Fatalf("no files: %q", got)
	}
	if got := names("geosite"); got != "geosite.dat " {
		t.Fatalf("named: %q", got)
	}
	if err := os.WriteFile(files[1].Path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(""); got != "geoip.dat geosite.dat " {
		t.Fatalf("geosite present: %q", got)
	}
	if _, err := PickGeoFiles(files, "nope"); err == nil {
		t.Fatal("unknown name accepted")
	}
}
