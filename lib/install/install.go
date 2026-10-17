package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/c4milo/unpackit"
	"github.com/djylb/netx"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/version"
)

var BuildTarget string

// SysvScript Keep it in sync with the template from service_sysv_linux.go file
// Use "ps | grep -v grep | grep $(get_pid)" because "ps PID" may not work on OpenWrt
const SysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{.Description}}
# processname: {{.Path}}
### BEGIN INIT INFO
# Provides:          {{.Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO
cmd="{{.Path}}{{range .Arguments}} {{.|cmd}}{{end}}"
name=$(basename $(readlink -f $0))
pid_file="/var/run/$name.pid"
stdout_log="/var/log/$name.log"
stderr_log="/var/log/$name.err"
[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name
get_pid() {
    cat "$pid_file"
}
is_running() {
    [ -f "$pid_file" ] && ps | grep -v grep | grep $(get_pid) > /dev/null 2>&1
}
case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if .WorkingDirectory}}cd '{{.WorkingDirectory}}'{{end}}
            $cmd >> "$stdout_log" 2>> "$stderr_log" &
            echo $! > "$pid_file"
            if ! is_running; then
                echo "Unable to start, see $stdout_log and $stderr_log"
                exit 1
            fi
        fi
    ;;
    stop)
        if is_running; then
            echo -n "Stopping $name.."
            kill $(get_pid)
            for i in $(seq 1 10)
            do
                if ! is_running; then
                    break
                fi
                echo -n "."
                sleep 1
            done
            echo
            if is_running; then
                echo "Not stopped; may still be shutting down or shutdown may have failed"
                exit 1
            else
                echo "Stopped"
                if [ -f "$pid_file" ]; then
                    rm "$pid_file"
                fi
            fi
        else
            echo "Not running"
        fi
    ;;
    restart)
        $0 stop
        if is_running; then
            echo "Unable to stop, will not attempt to start"
            exit 1
        fi
        $0 start
    ;;
    status)
        if is_running; then
            echo "Running"
        else
            echo "Stopped"
            exit 1
        fi
    ;;
    *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
`

const SystemdScript = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}} {{end}}
[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:/var/log/{{.Name}}.out
StandardError=file:/var/log/{{.Name}}.err
{{- end}}
Restart=always
RestartSec=120
[Install]
WantedBy=multi-user.target
`

// systemBinDirs are the directories that install places the executable in,
// in order of preference.
var systemBinDirs = []string{"/usr/bin", "/usr/local/bin"}

// UpdateNps replaces the nps executables with the latest release and then
// refreshes the web assets of the current run directory. It returns the
// replaced executables; none with a nil error means the running version is
// already the latest.
func UpdateNps() ([]string, error) {
	return update("server", "nps")
}

// UpdateNpc replaces the npc executables with the latest release. It returns
// the replaced executables; none with a nil error means the running version
// is already the latest.
func UpdateNpc() ([]string, error) {
	return update("client", "npc")
}

func update(kind, bin string) ([]string, error) {
	RemoveStaleExecutables()

	c := newHTTPClient()
	filename := packageName(kind)
	var (
		tag    string
		digest string
		urls   []string
	)
	rl, verified, err := fetchLatestRelease(c)
	if err != nil {
		fmt.Println("Query latest release failed:", err)
		fmt.Println("Falling back to the CDN @latest mirrors; their packages carry no SHA-256 digest")
		urls = []string{
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://github.com/djylb/nps/releases/latest/download/%s", filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
		}
	} else {
		if !verified {
			fmt.Println("WARNING: the release metadata was fetched without certificate verification, so its SHA-256 digests are unverified as well; they only guard against transfer corruption, not against tampering")
		}
		tag = rl.TagName
		fmt.Println("The latest version is", tag)
		if version.Compare(tag, version.VERSION) <= 0 {
			fmt.Println("Already up to date, current version is", version.VERSION)
			return nil, nil
		}
		if digest, err = rl.sha256(filename); err != nil {
			return nil, err
		}
		if digest == "" {
			fmt.Println("No SHA-256 digest published for", filename)
		}
		urls = []string{
			fmt.Sprintf("https://github.com/djylb/nps/releases/download/%s/%s", tag, filename),
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", tag, filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", tag, filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", tag, filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", tag, filename),
		}
	}

	tmpDir, err := os.MkdirTemp("", "nps-update-")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	pkgDir := filepath.Join(tmpDir, "pkg")
	if err := downloadPackage(c, urls, digest, filepath.Join(tmpDir, filename), pkgDir); err != nil {
		return nil, err
	}
	src := filepath.Join(pkgDir, executableName(bin))
	if ok, err := pathExists(src); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%s not found in the release package", executableName(bin))
	}
	if tag == "" {
		if !packageIsNewer(src) {
			fmt.Println("Already up to date, current version is", version.VERSION)
			return nil, nil
		}
		tag = "latest"
	}

	targets, err := executableTargets(bin)
	if err != nil {
		return nil, err
	}
	if err := replaceExecutables(src, targets, common.IsWindows()); err != nil {
		return nil, err
	}
	for _, t := range targets {
		fmt.Println("Replaced", t)
	}
	if bin == "nps" {
		if err := copyServerAssets(pkgDir, common.GetRunPath()); err != nil {
			return targets, fmt.Errorf("executables replaced, but updating web assets failed: %w", err)
		}
	}
	fmt.Println("Update completed:", tag)
	return targets, nil
}

// packageIsNewer reports whether the downloaded executable is newer than the
// running one. It asks the executable for its version and, when that fails,
// treats it as new unless its content equals the running executable.
func packageIsNewer(src string) bool {
	v, err := executableVersion(src)
	if err == nil {
		fmt.Println("The downloaded version is", v)
		return version.Compare(v, version.VERSION) > 0
	}
	fmt.Println("Cannot read the downloaded version:", err)
	exe, err := currentExecutable()
	if err != nil {
		return true
	}
	a, errA := fileSHA256(src)
	b, errB := fileSHA256(exe)
	return errA != nil || errB != nil || a != b
}

func executableVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Version:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", errors.New("no version in output")
}

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

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

// sha256 returns the hex SHA-256 digest published for the named asset, or ""
// when the release publishes none.
func (r *release) sha256(name string) (string, error) {
	for _, a := range r.Assets {
		if a.Name != name {
			continue
		}
		if a.Digest == "" {
			return "", nil
		}
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if raw, err := hex.DecodeString(sum); !ok || err != nil || len(raw) != sha256.Size {
			return "", fmt.Errorf("release %s has a malformed digest for %s: %q", r.TagName, name, a.Digest)
		}
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("release %s has no asset %s", r.TagName, name)
}

// httpClient sends requests with certificate verification against the system
// roots and retries a request without verification only when the certificate
// check itself failed, so devices with a broken CA store can still update.
type httpClient struct {
	verified   *http.Client
	unverified *http.Client
}

func newHTTPClient() *httpClient {
	return &httpClient{
		verified:   newTransportClient(false),
		unverified: newTransportClient(true),
	}
}

func newTransportClient(insecure bool) *http.Client {
	const timeout = 5 * time.Second
	const idleTimeout = 10 * time.Second
	const keepAliveTime = 30 * time.Second

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return netx.NewTimeoutConn(raw, idleTimeout), nil
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				_ = raw.Close()
				return nil, err
			}
			cfg := &tls.Config{ServerName: host, InsecureSkipVerify: insecure}
			hsCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			tlsConn, err := tlsconn.Client(hsCtx, raw, cfg)
			if err != nil {
				return nil, err
			}
			return netx.NewTimeoutConn(tlsConn, idleTimeout), nil
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	if insecure {
		// requests through an HTTPS proxy use TLSClientConfig instead of DialTLSContext
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: transport}
}

// do sends req and reports whether the server certificate was verified.
func (c *httpClient) do(req *http.Request) (*http.Response, bool, error) {
	// The response is returned to the caller, which closes its body.
	//noinspection GoResourceLeak
	resp, err := c.verified.Do(req)
	if err == nil || !isCertificateError(err) {
		return resp, err == nil, err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	fmt.Printf("WARNING: certificate verification for %s failed: %v\n", req.URL.Host, err)
	fmt.Println("WARNING: the system certificate store may be broken or outdated; retrying this download WITHOUT certificate verification")
	resp, err = c.unverified.Do(req)
	return resp, false, err
}

func isCertificateError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var authErr x509.UnknownAuthorityError
	var invalidErr x509.CertificateInvalidError
	var hostErr x509.HostnameError
	return errors.As(err, &verifyErr) || errors.As(err, &authErr) ||
		errors.As(err, &invalidErr) || errors.As(err, &hostErr)
}

// fetchLatestRelease queries the latest release and reports whether the
// server certificate was verified.
func fetchLatestRelease(c *httpClient) (*release, bool, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/djylb/nps/releases/latest", nil)
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

// packageName returns the release archive built for this binary. It must match
// the names produced by build.assets.sh.
func packageName(kind string) string {
	osName := runtime.GOOS
	archName := runtime.GOARCH
	switch {
	case BuildTarget == "win7":
		return fmt.Sprintf("%s_%s_%s_old.tar.gz", osName, archName, kind)
	case BuildTarget != "":
		return fmt.Sprintf("%s_%s_%s_%s.tar.gz", osName, archName, BuildTarget, kind)
	default:
		return fmt.Sprintf("%s_%s_%s.tar.gz", osName, archName, kind)
	}
}

// downloadPackage fetches the archive from the first mirror that passes
// downloadArchive and unpacks cleanly into pkgDir.
func downloadPackage(c *httpClient, urls []string, digest, archive, pkgDir string) error {
	var lastErr error
	for _, url := range urls {
		fmt.Println("Trying:", url)
		err := downloadArchive(c, url, archive, digest)
		if err == nil {
			err = unpackArchive(archive, pkgDir)
		}
		if err != nil {
			fmt.Println("  → failed:", err)
			_ = os.RemoveAll(pkgDir)
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("all mirrors failed; last error: %w", lastErr)
}

func unpackArchive(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := unpackit.Unpack(f, dest); err != nil {
		return fmt.Errorf("unpack %s: %w", filepath.Base(archive), err)
	}
	return nil
}

// downloadArchive writes the body of url to dest. With a digest the content
// must match it. Without one the content is only trusted when the connection
// passed certificate verification.
func downloadArchive(c *httpClient, url, dest, digest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, verified, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if digest == "" && !verified {
		return errors.New("no SHA-256 digest and the certificate could not be verified; refusing an unauthenticated package")
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if digest == "" {
		fmt.Println("  → no SHA-256 digest; trusting the verified TLS connection")
		return nil
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != digest {
		return fmt.Errorf("checksum mismatch: got %s, want %s", sum, digest)
	}
	if !verified {
		fmt.Println("  → downloaded without certificate verification; SHA-256 digest matched")
	}
	return nil
}

// copyServerAssets copies the web UI and the reference config from srcDir to
// destDir. Each item is handled on its own: an item missing from srcDir keeps
// the copy already in destDir and is an error only when destDir has none.
func copyServerAssets(srcDir, destDir string) error {
	items := []struct {
		src, dest string
		dir       bool
	}{
		{src: "web/views", dest: "web/views", dir: true},
		{src: "web/static", dest: "web/static", dir: true},
		{src: "conf/nps.conf", dest: "conf/nps.conf.default"},
	}
	for _, it := range items {
		src := filepath.Join(srcDir, filepath.FromSlash(it.src))
		dest := filepath.Join(destDir, filepath.FromSlash(it.dest))
		if ok, err := pathExists(src); err != nil {
			return err
		} else if !ok {
			if exists, _ := pathExists(dest); exists {
				log.Printf("%s not found in %s, keep %s", it.src, srcDir, dest)
				continue
			}
			return fmt.Errorf("%s not found in %s", it.src, srcDir)
		}
		var err error
		if it.dir {
			err = CopyDir(src, dest)
		} else {
			_, err = copyFile(src, dest)
		}
		if err != nil {
			return err
		}
		chMod(dest, 0766)
	}
	return nil
}

// executableTargets returns the running executable and, on Unix, the copies
// install placed in the system bin directories.
func executableTargets(bin string) ([]string, error) {
	exe, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	targets := []string{exe}
	if !common.IsWindows() {
		for _, dir := range systemBinDirs {
			p, err := filepath.EvalSymlinks(filepath.Join(dir, bin))
			if err != nil {
				continue
			}
			if !containsFile(targets, p) {
				targets = append(targets, p)
			}
		}
	}
	return targets, nil
}

// installExecutable copies the executable found in srcDir to its install
// location and returns that path. Windows keeps the executable where it is.
// Helper copies named <bin>-update left by older installs are removed.
func installExecutable(srcDir, bin string) (string, error) {
	if common.IsWindows() {
		exe, err := currentExecutable()
		if err != nil {
			return "", err
		}
		_ = os.Remove(filepath.Join(filepath.Dir(exe), bin+"-update.exe"))
		return exe, nil
	}
	for _, dir := range systemBinDirs {
		_ = os.Remove(filepath.Join(dir, bin+"-update"))
	}
	src := filepath.Join(srcDir, bin)
	var errs []error
	for _, dir := range systemBinDirs {
		dest := filepath.Join(dir, bin)
		target := dest
		if p, err := filepath.EvalSymlinks(dest); err == nil {
			target = p
		}
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = replaceExecutables(src, []string{target}, false)
		}
		if err == nil {
			return dest, nil
		}
		errs = append(errs, fmt.Errorf("install %s: %w", dest, err))
	}
	return "", errors.Join(errs...)
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func executableName(bin string) string {
	if common.IsWindows() {
		return bin + ".exe"
	}
	return bin
}

func containsFile(paths []string, p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	for _, q := range paths {
		if qi, err := os.Stat(q); err == nil && os.SameFile(fi, qi) {
			return true
		}
	}
	return false
}

// stagedExecutable is one replacement of replaceExecutables.
type stagedExecutable struct {
	dest   string
	staged string
	backup string
}

// replaceExecutables replaces every dest with a copy of src as one unit. All
// copies are first written next to their dest; only when every copy is in
// place are they renamed over their dest, so a running executable is never
// truncated. If a rename fails, the executables already replaced are
// restored. Each dest is backed up as *.old before its rename: a hard link
// (or copy) on Unix, and with aside set the renamed original itself, since
// Windows refuses to replace a running image. Backups are removed on success;
// a still running Windows image is removed later by RemoveStaleExecutables.
func replaceExecutables(src string, dests []string, aside bool) error {
	staged, err := stageExecutables(src, dests)
	if err != nil {
		return err
	}
	return commitExecutables(staged, aside)
}

// stageExecutables writes a copy of src next to every dest that is not src.
func stageExecutables(src string, dests []string) ([]*stagedExecutable, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	var staged []*stagedExecutable
	for _, dest := range dests {
		if fi, err := os.Stat(dest); err == nil && os.SameFile(srcInfo, fi) {
			continue
		}
		s := &stagedExecutable{dest: dest, staged: dest + ".new"}
		staged = append(staged, s)
		if err := writeExecutable(src, s.staged); err != nil {
			removeStaged(staged)
			return nil, fmt.Errorf("stage %s: %w", dest, err)
		}
	}
	return staged, nil
}

// commitExecutables renames the staged copies over their dest, restoring the
// already replaced ones if any rename fails.
func commitExecutables(staged []*stagedExecutable, aside bool) error {
	for i, s := range staged {
		if err := s.commit(aside); err != nil {
			removeStaged(staged)
			err = fmt.Errorf("replace %s: %w", s.dest, err)
			for j := i - 1; j >= 0; j-- {
				if rbErr := staged[j].restore(); rbErr != nil {
					err = fmt.Errorf("%w; restore %s failed: %v", err, staged[j].dest, rbErr)
				}
			}
			return err
		}
	}
	for _, s := range staged {
		if s.backup != "" {
			_ = os.Remove(s.backup)
		}
	}
	return nil
}

func removeStaged(staged []*stagedExecutable) {
	for _, s := range staged {
		_ = os.Remove(s.staged)
	}
}

func (s *stagedExecutable) commit(aside bool) error {
	backup, err := backupExecutable(s.dest, aside)
	if err != nil {
		return err
	}
	s.backup = backup
	if err := os.Rename(s.staged, s.dest); err != nil {
		if rbErr := s.restore(); rbErr != nil {
			return fmt.Errorf("%w; restore failed: %v", err, rbErr)
		}
		return err
	}
	return nil
}

// restore puts the original dest back, or removes dest if there was none.
func (s *stagedExecutable) restore() error {
	if s.backup == "" {
		if err := os.Remove(s.dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	// renaming a hard link over its own target is a no-op that keeps both names
	if bi, err := os.Stat(s.backup); err == nil {
		if di, err := os.Stat(s.dest); err == nil && os.SameFile(bi, di) {
			backup := s.backup
			s.backup = ""
			return os.Remove(backup)
		}
	}
	if err := os.Rename(s.backup, s.dest); err != nil {
		return err
	}
	s.backup = ""
	return nil
}

// backupExecutable preserves dest as a *.old file and returns its path, or ""
// when dest does not exist. With aside set dest itself is moved.
func backupExecutable(dest string, aside bool) (string, error) {
	if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	backup := asidePath(dest)
	if aside {
		if err := os.Rename(dest, backup); err != nil {
			return "", err
		}
		return backup, nil
	}
	if err := os.Link(dest, backup); err == nil {
		return backup, nil
	}
	if err := writeExecutable(dest, backup); err != nil {
		_ = os.Remove(backup)
		return "", err
	}
	return backup, nil
}

func writeExecutable(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !common.IsWindows() {
		return os.Chmod(dest, 0o755)
	}
	return nil
}

// asidePath returns a free name to move dest to. A locked dest.old left by an
// update whose process is still running gets a unique sibling instead.
func asidePath(dest string) string {
	old := dest + ".old"
	if err := os.Remove(old); err == nil || errors.Is(err, fs.ErrNotExist) {
		return old
	}
	return dest + "." + strconv.FormatInt(time.Now().UnixNano(), 36) + ".old"
}

// RemoveStaleExecutables deletes executables that an earlier update moved
// aside on Windows. They stay locked until the process running them exits, so
// failures are ignored and retried on the next start or update.
func RemoveStaleExecutables() {
	if !common.IsWindows() {
		return
	}
	if exe, err := currentExecutable(); err == nil {
		removeAsideFiles(exe)
	}
}

func removeAsideFiles(path string) {
	dir, base := filepath.Split(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, base+".") && strings.HasSuffix(name, ".old") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// NPC installs the npc executable and returns the path the service should run.
func NPC() (string, error) {
	if err := os.MkdirAll(common.GetInstallPath(), 0o755); err != nil {
		return "", err
	}
	return installExecutable(common.GetAppPath(), "npc")
}

// NPS installs the nps executable, web assets and, on a fresh install, the
// config directory. It returns the path the service should run.
func NPS() (string, error) {
	path := common.GetInstallPath()
	appPath := common.GetAppPath()
	log.Println("install path:" + path)
	if exists, err := pathExists(path); err != nil {
		return "", err
	} else if !exists {
		// keep the existing config when reinstalling
		if err := CopyDir(filepath.Join(appPath, "conf"), filepath.Join(path, "conf")); err != nil {
			return "", err
		}
		chMod(filepath.Join(path, "conf"), 0766)
	}
	if err := copyServerAssets(appPath, path); err != nil {
		return "", err
	}
	binPath, err := installExecutable(appPath, "nps")
	if err != nil {
		return "", err
	}
	log.Println("install ok!")
	log.Println("Static files and configuration files in the current directory will be useless")
	log.Println("The new configuration file is located in", path, "you can edit them")
	if !common.IsWindows() {
		log.Println(`You can start with:
nps start|stop|restart|uninstall|update
anywhere!`)
	} else {
		log.Println(`You can start with:
nps.exe start|stop|restart|uninstall|update
now!`)
	}
	chMod(common.GetLogPath(), 0777)
	return binPath, nil
}

// CopyDir copies the files under srcPath into destPath, creating directories
// as needed.
func CopyDir(srcPath string, destPath string) error {
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return errors.New("srcPath is not a directory")
	}
	if destInfo, err := os.Stat(destPath); err == nil {
		if !destInfo.IsDir() {
			return errors.New("destPath is not a directory")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return filepath.WalkDir(srcPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcPath, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destPath, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		log.Println("copy file: " + path + " -> " + target)
		if _, err := copyFile(path, target); err != nil {
			return err
		}
		chMod(target, 0766)
		return nil
	})
}

// copyFile copies src to dest, creating the parent directory. Copying a file
// onto itself is a no-op.
func copyFile(src, dest string) (int64, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if destInfo, err := os.Stat(dest); err == nil && os.SameFile(srcInfo, destInfo) {
		return 0, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// 检测文件夹路径是否存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func chMod(name string, mode os.FileMode) {
	if !common.IsWindows() {
		_ = os.Chmod(name, mode)
	}
}
