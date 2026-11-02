package install

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/selfupdate"
)

// systemBinDirs are the directories that install places the executable in,
// in order of preference.
var systemBinDirs = []string{"/usr/bin", "/usr/local/bin"}

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
			err = installFile(src, target)
		}
		if err == nil {
			return dest, nil
		}
		errs = append(errs, fmt.Errorf("install %s: %w", dest, err))
	}
	return "", errors.Join(errs...)
}

// installFile replaces target with a copy of the executable src through the
// update journal of target, so that a running target is never truncated and
// an interrupted install is undone on the next start. Installing a file
// over itself does nothing.
func installFile(src, target string) error {
	if same, _ := sameFile(src, target); same {
		return nil
	}
	ctx := context.Background()
	staged, err := selfupdate.JournalFor(target).Stage(ctx, target)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Abort(ctx) }()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(staged.Paths()[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
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
	return staged.Replace(ctx, selfupdate.ReplaceOptions{Version: version.VERSION})
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

// renameRetry is how long renameFile keeps retrying on Windows, where a
// scanner or indexer briefly opening a file makes renaming it fail.
const renameRetry = 2 * time.Second

func renameFile(from, to string) error {
	err := os.Rename(from, to)
	if err == nil || !common.IsWindows() || errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for deadline := time.Now().Add(renameRetry); time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		if err = os.Rename(from, to); err == nil {
			return nil
		}
	}
	return err
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
	if err := SyncGeoData(filepath.Join(appPath, "conf"), filepath.Join(path, "conf", "geoip.dat"), filepath.Join(path, "conf", "geosite.dat")); err != nil {
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
