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

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"

	"github.com/djylb/selfupdate"
)

var geoLog = slogx.Module("geo")

// geoRepo publishes the geoip.dat and geosite.dat the access lists use.
const geoRepo = "Loyalsoldier/v2ray-rules-dat"

// GeoFile is one geodata file: its name in the release and its local path.
type GeoFile struct {
	Name string
	Path string
}

// GeoFiles returns the geodata files kept at ipPath and sitePath.
func GeoFiles(ipPath, sitePath string) []GeoFile {
	return []GeoFile{{Name: "geoip.dat", Path: ipPath}, {Name: "geosite.dat", Path: sitePath}}
}

// PickGeoFiles returns the file called name, geoip or geosite. With an empty
// name it returns geoip and the others that exist: having a file is what
// turns its rules on, and only geoip is fetched without being asked for.
func PickGeoFiles(files []GeoFile, name string) ([]GeoFile, error) {
	var out []GeoFile
	for _, f := range files {
		if name != "" {
			if f.Name == name+".dat" {
				out = append(out, f)
			}
			continue
		}
		if _, err := os.Stat(f.Path); err == nil || f.Name == "geoip.dat" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("unknown geodata file %q, want geoip or geosite", name)
	}
	return out, nil
}

// GeoUpdate is the result of UpdateGeoData.
type GeoUpdate struct {
	// Updated lists the files that were replaced.
	Updated []string
}

// geoVersionPath returns the file that records the release the geodata file
// at path comes from.
func geoVersionPath(path string) string { return path + ".version" }

// GeoDataVersion returns the release the geodata file at path comes from, ""
// when unknown.
func GeoDataVersion(path string) string {
	b, err := os.ReadFile(geoVersionPath(path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// UpdateGeoData downloads the latest release of each of files. A file whose
// content already matches the release is left alone. Each file is checked
// against its SHA-256 digest and replaced atomically, on its own: one that
// fails leaves the others updated.
func UpdateGeoData(files []GeoFile) (*GeoUpdate, error) {
	c := newHTTPClient()
	res := &GeoUpdate{}
	rl, verified, err := fetchLatestRelease(c, geoRepo)
	if err != nil {
		fmt.Println("Query latest geodata release failed:", err)
		fmt.Println("Falling back to the CDN mirrors, checked against their published SHA-256 sums")
		rl = nil
	} else if !verified {
		fmt.Println("WARNING: the geodata release metadata was fetched without certificate verification")
	}
	var errs []error
	for _, f := range files {
		if err := updateGeoFile(c, rl, f, res); err != nil {
			slogx.Emit(context.Background(), geoLog, &logevent.SourceFailed, "source", f.Name, "reason", "update_failed", "err", err)
			errs = append(errs, fmt.Errorf("update %s: %w", f.Name, err))
		}
	}
	return res, errors.Join(errs...)
}

// updateGeoFile brings f to the release rl, or to the CDN mirrors when rl is
// nil.
func updateGeoFile(c *httpClient, rl *release, f GeoFile, res *GeoUpdate) error {
	var sources []geoSource
	if rl == nil {
		var err error
		if sources, err = mirrorSources(c, f.Name); err != nil {
			return err
		}
	} else {
		digest, size, err := rl.asset(f.Name)
		if err != nil {
			return err
		}
		if digest == "" {
			return fmt.Errorf("release %s publishes no SHA-256 digest", rl.TagName)
		}
		for _, url := range []string{
			fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", geoRepo, rl.TagName, f.Name),
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/%s@release/%s", geoRepo, f.Name),
		} {
			sources = append(sources, geoSource{url: url, digest: digest, size: size})
		}
	}
	// The first source tells the content to have; the mirrors may lag
	// behind it.
	if _, d, err := selfupdate.DigestFile(f.Path); err == nil && hex.EncodeToString(d.SHA256[:]) == sources[0].digest {
		fmt.Println(f.Name, "is up to date")
	} else {
		if err := replaceGeoFile(f.Path, func(staged string) error { return downloadGeoFile(c, sources, staged) }); err != nil {
			return err
		}
		slogx.Emit(context.Background(), geoLog, &logevent.SourceUpdated, "source", f.Name, "path", f.Path)
		res.Updated = append(res.Updated, f.Path)
	}
	if rl == nil {
		// The mirrors do not tell the release.
		_ = os.Remove(geoVersionPath(f.Path))
		return nil
	}
	return os.WriteFile(geoVersionPath(f.Path), []byte(rl.TagName+"\n"), 0o644)
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

// replaceGeoFile writes the new content of the file at path with write, to a
// staged file next to it, and swaps it in, so that a reader sees either the
// old file or the new one. An update interrupted by a crash is undone, or its
// staged file deleted, by the next one.
func replaceGeoFile(path string, write func(staged string) error) error {
	ctx := context.Background()
	j := selfupdate.JournalFor(path)
	if _, err := j.Recover(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	st, err := j.Stage(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Abort(ctx) }()
	staged := st.Paths()[0]
	if err := write(staged); err != nil {
		return err
	}
	// A file that replaces another takes its permissions; a new one is
	// readable by all.
	if err := os.Chmod(staged, 0o644); err != nil {
		return err
	}
	return st.Replace(ctx, selfupdate.ReplaceOptions{})
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
