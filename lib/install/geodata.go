package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// geoRepo publishes the geoip.dat and geosite.dat the access lists use.
const geoRepo = "Loyalsoldier/v2ray-rules-dat"

// geoVersionFile, next to geoip.dat, records the release the geodata files
// come from, so that the files of a package replace only older ones.
const geoVersionFile = "geodata.version"

// geoIPPath and geoSitePath are where nps keeps its geodata files; see
// SetGeoDataPaths.
var geoIPPath, geoSitePath string

// SetGeoDataPaths sets where nps keeps its geodata files, for update to
// refresh them from the release package.
func SetGeoDataPaths(ipPath, sitePath string) {
	geoIPPath, geoSitePath = ipPath, sitePath
}

func geoDataPaths(runPath string) (string, string) {
	if geoIPPath != "" && geoSitePath != "" {
		return geoIPPath, geoSitePath
	}
	return filepath.Join(runPath, "conf", "geoip.dat"), filepath.Join(runPath, "conf", "geosite.dat")
}

// GeoFile is one geodata file: its name in the release and its local path.
type GeoFile struct {
	Name string
	Path string
}

// GeoFiles returns the geodata files kept at ipPath and sitePath.
func GeoFiles(ipPath, sitePath string) []GeoFile {
	return []GeoFile{{Name: "geoip.dat", Path: ipPath}, {Name: "geosite.dat", Path: sitePath}}
}

// GeoUpdate is the result of UpdateGeoData.
type GeoUpdate struct {
	// Version is the release the files now come from, "" when unknown.
	Version string
	// Updated lists the files that were replaced.
	Updated []string
}

// GeoDataVersion returns the release the geodata files next to ipPath come
// from, "" when unknown.
func GeoDataVersion(ipPath string) string {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ipPath), geoVersionFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeGeoDataVersion(ipPath, version string) error {
	if version == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(filepath.Dir(ipPath), geoVersionFile), []byte(version+"\n"), 0o644)
}

// UpdateGeoData downloads the latest geoip.dat and geosite.dat to ipPath and
// sitePath. A file whose content already matches the release is left alone.
// Each file is checked against its SHA-256 digest and replaced atomically,
// so that a reader never sees a partial file.
func UpdateGeoData(ipPath, sitePath string) (*GeoUpdate, error) {
	c := newHTTPClient()
	files := GeoFiles(ipPath, sitePath)
	res := &GeoUpdate{}
	rl, verified, err := fetchLatestRelease(c, geoRepo)
	if err != nil {
		fmt.Println("Query latest geodata release failed:", err)
		fmt.Println("Falling back to the CDN mirrors, checked against their published SHA-256 sums")
		for _, f := range files {
			updated, err := updateGeoFileFromMirrors(c, f)
			if err != nil {
				return res, fmt.Errorf("update %s: %w", f.Name, err)
			}
			if updated {
				res.Updated = append(res.Updated, f.Path)
			}
		}
		// The mirrors do not tell the release; an unknown version lets the
		// files of the next package replace these.
		_ = os.Remove(filepath.Join(filepath.Dir(ipPath), geoVersionFile))
		return res, nil
	}
	if !verified {
		fmt.Println("WARNING: the geodata release metadata was fetched without certificate verification")
	}
	for _, f := range files {
		digest, err := rl.sha256(f.Name)
		if err != nil {
			return res, err
		}
		if digest == "" {
			return res, fmt.Errorf("release %s publishes no SHA-256 digest for %s", rl.TagName, f.Name)
		}
		if sum, err := fileSHA256(f.Path); err == nil && sum == digest {
			fmt.Println(f.Name, "is up to date")
			continue
		}
		urls := []string{
			fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", geoRepo, rl.TagName, f.Name),
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
		}
		if err := downloadGeoFile(c, urls, digest, f.Path); err != nil {
			return res, fmt.Errorf("update %s: %w", f.Name, err)
		}
		res.Updated = append(res.Updated, f.Path)
	}
	res.Version = rl.TagName
	if err := writeGeoDataVersion(ipPath, rl.TagName); err != nil {
		return res, err
	}
	return res, nil
}

// updateGeoFileFromMirrors updates f from the CDN mirrors of the release
// branch, checked against the sha256sum file published next to it. The
// mirror may lag behind the latest release; a matching digest means the
// content is the one it publishes.
func updateGeoFileFromMirrors(c *httpClient, f GeoFile) (bool, error) {
	var lastErr error
	for _, base := range []string{"https://cdn.jsdelivr.net", "https://fastly.jsdelivr.net", "https://gcore.jsdelivr.net", "https://testingcf.jsdelivr.net"} {
		url := fmt.Sprintf("%s/gh/%s@release/%s", base, geoRepo, f.Name)
		digest, err := fetchSHA256Sum(c, url+".sha256sum")
		if err != nil {
			lastErr = err
			continue
		}
		if sum, err := fileSHA256(f.Path); err == nil && sum == digest {
			fmt.Println(f.Name, "is up to date")
			return false, nil
		}
		if err := downloadGeoFile(c, []string{url}, digest, f.Path); err != nil {
			lastErr = err
			continue
		}
		return true, nil
	}
	return false, lastErr
}

// fetchSHA256Sum fetches a sha256sum file and returns its digest. It insists
// on a verified certificate, since the digest is what authenticates the
// download.
func fetchSHA256Sum(c *httpClient, url string) (string, error) {
	resp, err := c.verified.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	if raw, err := hex.DecodeString(sum); err != nil || len(raw) != sha256.Size {
		return "", fmt.Errorf("%s: malformed digest", url)
	}
	return strings.ToLower(sum), nil
}

// downloadGeoFile downloads the first of urls that succeeds and matches
// digest to dest, replacing it atomically.
func downloadGeoFile(c *httpClient, urls []string, digest, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".download"
	defer func() { _ = os.Remove(tmp) }()
	var errs []error
	for _, url := range urls {
		fmt.Println("Trying:", url)
		if err := downloadArchive(c, url, tmp, digest); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", url, err))
			continue
		}
		if err := renameFile(tmp, dest); err != nil {
			return err
		}
		fmt.Println("Updated", dest)
		return nil
	}
	return errors.Join(errs...)
}

// SyncGeoData copies the geodata files of an unpacked release in srcConf to
// ipPath and sitePath when the release has newer ones than those in place,
// or when a file is missing. Newer files from UpdateGeoData are kept.
func SyncGeoData(srcConf, ipPath, sitePath string) error {
	pkgVersion := GeoDataVersion(filepath.Join(srcConf, "geoip.dat"))
	localVersion := GeoDataVersion(ipPath)
	newer := pkgVersion != "" && pkgVersion > localVersion
	copied := false
	for _, f := range GeoFiles(ipPath, sitePath) {
		src := filepath.Join(srcConf, f.Name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if same, _ := sameFile(src, f.Path); same {
			continue
		}
		if _, err := os.Stat(f.Path); err == nil && !newer {
			continue
		}
		if err := copyGeoFile(src, f.Path); err != nil {
			return fmt.Errorf("copy %s: %w", f.Name, err)
		}
		fmt.Println("Updated", f.Path, "from the release package")
		copied = true
	}
	if copied && newer {
		return writeGeoDataVersion(ipPath, pkgVersion)
	}
	return nil
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// copyGeoFile copies src over dest through a temporary file, so that a
// reader never sees a partial file.
func copyGeoFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".download"
	defer func() { _ = os.Remove(tmp) }()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return renameFile(tmp, dest)
}

// fileSHA256 returns the hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
