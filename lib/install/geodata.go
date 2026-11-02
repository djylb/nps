package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/djylb/selfupdate"
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
// Each file is checked against its SHA-256 digest, and the files are
// replaced together and atomically, so that a reader never sees a partial
// file or a mix of old and new files.
func UpdateGeoData(ipPath, sitePath string) (*GeoUpdate, error) {
	c := newHTTPClient()
	files := GeoFiles(ipPath, sitePath)
	res := &GeoUpdate{}
	rl, verified, err := fetchLatestRelease(c, geoRepo)
	sources := make([][]geoSource, len(files))
	if err != nil {
		fmt.Println("Query latest geodata release failed:", err)
		fmt.Println("Falling back to the CDN mirrors, checked against their published SHA-256 sums")
		for i, f := range files {
			if sources[i], err = mirrorSources(c, f.Name); err != nil {
				return res, fmt.Errorf("update %s: %w", f.Name, err)
			}
		}
	} else {
		if !verified {
			fmt.Println("WARNING: the geodata release metadata was fetched without certificate verification")
		}
		for i, f := range files {
			digest, size, err := rl.asset(f.Name)
			if err != nil {
				return res, err
			}
			if digest == "" {
				return res, fmt.Errorf("release %s publishes no SHA-256 digest for %s", rl.TagName, f.Name)
			}
			for _, url := range []string{
				fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", geoRepo, rl.TagName, f.Name),
				fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
				fmt.Sprintf("https://fastly.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
			} {
				sources[i] = append(sources[i], geoSource{url: url, digest: digest, size: size})
			}
		}
	}
	var stale []GeoFile
	var staleSources [][]geoSource
	for i, f := range files {
		// The first source tells the content to have; the mirrors may lag
		// behind it.
		if _, d, err := selfupdate.DigestFile(f.Path); err == nil && hex.EncodeToString(d.SHA256[:]) == sources[i][0].digest {
			fmt.Println(f.Name, "is up to date")
			continue
		}
		stale = append(stale, f)
		staleSources = append(staleSources, sources[i])
	}
	if len(stale) > 0 {
		err := replaceGeoFiles(ipPath, stale, func(i int, staged string) error {
			if err := downloadGeoFile(c, staleSources[i], staged); err != nil {
				return fmt.Errorf("update %s: %w", stale[i].Name, err)
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		for _, f := range stale {
			fmt.Println("Updated", f.Path)
			res.Updated = append(res.Updated, f.Path)
		}
	}
	if rl == nil {
		// The mirrors do not tell the release; an unknown version lets the
		// files of the next package replace these.
		_ = os.Remove(filepath.Join(filepath.Dir(ipPath), geoVersionFile))
		return res, nil
	}
	res.Version = rl.TagName
	if err := writeGeoDataVersion(ipPath, rl.TagName); err != nil {
		return res, err
	}
	return res, nil
}

// geoSource is a place to download a geodata file from, with the SHA-256
// digest the file must have there and its size, 0 when unknown.
type geoSource struct {
	url    string
	digest string
	size   int64
}

// mirrorSources returns the CDN mirrors of the release branch that publish
// a sha256sum file for name, each checked against its own sum. The mirrors
// may lag behind the latest release; a matching digest means the content is
// the one the mirror publishes.
func mirrorSources(c *httpClient, name string) ([]geoSource, error) {
	var out []geoSource
	var errs []error
	for _, base := range []string{"https://cdn.jsdelivr.net", "https://fastly.jsdelivr.net", "https://gcore.jsdelivr.net", "https://testingcf.jsdelivr.net"} {
		url := fmt.Sprintf("%s/gh/%s@release/%s", base, geoRepo, name)
		digest, err := fetchSHA256Sum(c, url+".sha256sum")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, geoSource{url: url, digest: digest})
	}
	if len(out) == 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// SyncGeoData copies the geodata files of an unpacked release in srcConf to
// ipPath and sitePath when the release has newer ones than those in place,
// or when a file is missing. Newer files from UpdateGeoData are kept.
func SyncGeoData(srcConf, ipPath, sitePath string) error {
	pkgVersion := GeoDataVersion(filepath.Join(srcConf, "geoip.dat"))
	localVersion := GeoDataVersion(ipPath)
	newer := pkgVersion != "" && pkgVersion > localVersion
	var copies []GeoFile
	var srcs []string
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
		copies = append(copies, f)
		srcs = append(srcs, src)
	}
	if len(copies) == 0 {
		return nil
	}
	err := replaceGeoFiles(ipPath, copies, func(i int, staged string) error {
		if err := copyFileTo(srcs[i], staged); err != nil {
			return fmt.Errorf("copy %s: %w", copies[i].Name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range copies {
		fmt.Println("Updated", f.Path, "from the release package")
	}
	if newer {
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

// replaceGeoFiles writes the new content of each file with write, to a
// staged file next to it, and then swaps them all in at once, so that a
// reader sees either the old files or the new ones. The swap is recorded in a
// journal next to ipPath: an update interrupted by a crash is undone, or its
// staged files deleted, by the next one.
func replaceGeoFiles(ipPath string, files []GeoFile, write func(i int, staged string) error) error {
	ctx := context.Background()
	j := selfupdate.JournalFor(ipPath)
	if _, err := j.Recover(ctx); err != nil {
		return err
	}
	targets := make([]string, len(files))
	for i, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		targets[i] = f.Path
	}
	st, err := j.Stage(ctx, targets...)
	if err != nil {
		return err
	}
	defer func() { _ = st.Abort(ctx) }()
	for i, staged := range st.Paths() {
		if err := write(i, staged); err != nil {
			return err
		}
		// A file that replaces another takes its permissions; a new one is
		// readable by all, as the package installs it.
		if err := os.Chmod(staged, 0o644); err != nil {
			return err
		}
	}
	return st.Replace(ctx, selfupdate.ReplaceOptions{})
}

// copyFileTo copies the file src to the new file dst.
func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// asset returns the hex SHA-256 digest published for the named asset, ""
// when the release publishes none, and its size, 0 when unknown.
func (r *release) asset(name string) (string, int64, error) {
	for _, a := range r.Assets {
		if a.Name != name {
			continue
		}
		if a.Digest == "" {
			return "", a.Size, nil
		}
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if raw, err := hex.DecodeString(sum); !ok || err != nil || len(raw) != sha256.Size {
			return "", 0, fmt.Errorf("release %s has a malformed digest for %s: %q", r.TagName, name, a.Digest)
		}
		return strings.ToLower(sum), max(a.Size, 0), nil
	}
	return "", 0, fmt.Errorf("release %s has no asset %s", r.TagName, name)
}

// fetchLatestRelease fetches the latest release of the GitHub repository
// repo, owner/name, and reports whether the server certificate was verified.
func fetchLatestRelease(c *httpClient, repo string) (*release, bool, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, verified, err := c.do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("status %d", resp.StatusCode)
	}
	rl := new(release)
	if err := json.NewDecoder(resp.Body).Decode(rl); err != nil {
		return nil, false, err
	}
	if rl.TagName == "" {
		return nil, false, errors.New("release has no tag")
	}
	return rl, verified, nil
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

// downloadGeoFile downloads the first of sources that succeeds and matches
// its digest to dest.
func downloadGeoFile(c *httpClient, sources []geoSource, dest string) error {
	var errs []error
	for _, src := range sources {
		fmt.Println("Trying:", src.url)
		if err := downloadChecked(c, src, dest); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.url, err))
			continue
		}
		return nil
	}
	return errors.Join(errs...)
}

// downloadChecked downloads src to dest and checks it against the digest
// and the size of src, or the length the server announces when src has no
// size.
func downloadChecked(c *httpClient, src geoSource, dest string) error {
	req, err := http.NewRequest(http.MethodGet, src.url, nil)
	if err != nil {
		return err
	}
	resp, verified, err := c.do(req)
	if err != nil {
		return err
	}
	body := &responseSource{body: resp.Body}
	defer body.close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	size := src.size
	if size <= 0 {
		size = resp.ContentLength
	}
	if size <= 0 {
		return errors.New("size unknown")
	}
	f, err := selfupdate.Download(context.Background(), body, selfupdate.Artifact{
		Name:   filepath.Base(dest),
		URL:    src.url,
		SHA256: src.digest,
		Size:   size,
		Format: selfupdate.FormatRaw,
	}, dest, nil)
	if err != nil {
		return err
	}
	_ = f.Close()
	if !verified {
		fmt.Println("  → downloaded without certificate verification; SHA-256 digest matched")
	}
	return nil
}

// responseSource is a selfupdate.Source that serves the body of a response
// already received, from its start.
type responseSource struct {
	body io.ReadCloser
}

func (s *responseSource) Fetch(context.Context) ([]byte, error) {
	return nil, errors.ErrUnsupported
}

func (s *responseSource) Open(context.Context, selfupdate.Artifact, int64) (io.ReadCloser, int64, error) {
	if s.body == nil {
		return nil, 0, errors.New("response already read")
	}
	b := s.body
	s.body = nil
	return b, 0, nil
}

// close closes the body if Download did not take it.
func (s *responseSource) close() {
	if s.body != nil {
		_ = s.body.Close()
	}
}
