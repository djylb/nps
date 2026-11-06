package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/selfupdate"
	"github.com/shirou/gopsutil/v4/process"
)

// ReleaseSigningKey is the public key, in the "ed25519:..." form printed by
// "selfupdate keygen", whose private half signs the manifest.json of every
// release. Updates are installed only from manifests it signed; a build
// without it cannot update itself.
const ReleaseSigningKey = "ed25519:PcyApOO6AE0rjSIDLEUYJnO9lZLx/4XnzVwJDt5Pcz8="

// BuildTarget names the variant of a build for a CPU feature level or an
// old system, such as "v7" or "softfloat"; set by build.assets.sh.
var BuildTarget string

// releaseProduct is the product named in the release manifests.
const releaseProduct = "nps"

// manifestURLs locates the signed manifest, in the order tried. Artifacts are listed relative to the manifest, so each
// mirror serves them too.
var manifestURLs = []string{
	"https://github.com/djylb/nps/releases/latest/download/manifest.json",
	"https://cdn.jsdelivr.net/gh/djylb/nps-mirror@latest/manifest.json",
	"https://fastly.jsdelivr.net/gh/djylb/nps-mirror@latest/manifest.json",
	"https://gcore.jsdelivr.net/gh/djylb/nps-mirror@latest/manifest.json",
	"https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@latest/manifest.json",
}

// UpdateOptions selects how Update installs a release.
type UpdateOptions struct {
	// Force installs the latest release again when the running version is
	// that release, and retries a release that failed before. It lifts no
	// gate of the manifest without an unlock, and never installs an older
	// version.
	Force bool
	// Pending keeps the replaced files until the new version confirms
	// itself (see StartUpdate); a start that does not is rolled back. Set
	// it when the new version is started right after the update.
	Pending bool
}

// UpdateResult describes a completed update.
type UpdateResult struct {
	// Version is the version updated to.
	Version string
	// Replaced are the executables replaced with the new version.
	Replaced []string
	// Running are the other processes that ran a replaced executable when it
	// was replaced; they keep running the old version until restarted.
	Running []int32
}

// Update replaces the executables of bin ("nps" or "npc") with the latest
// release: the running one, on Unix the copies install placed in the system
// bin directories, and the one the system service runs. For nps the web
// assets and the reference config of the run directory are replaced along
// with them, and the geodata files when the release has newer ones. All
// files are replaced as one set, so an interrupted update is undone on the
// next start. A nil result with a nil error means the running version is
// already the latest.
func Update(ctx context.Context, bin, service string, o UpdateOptions) (*UpdateResult, error) {
	targets, err := executableTargets(bin, service)
	if err != nil {
		return nil, err
	}
	u, err := newUpdater(bin, o.Force)
	if err != nil {
		return nil, err
	}
	// The journal sits next to the executable the service runs, if any, as
	// that is the process that confirms or rolls back a pending update.
	primary := targets[0]
	if p := serviceExecutable(service); p != "" && containsFile(targets, p) {
		primary = p
	}
	u.Executable = primary
	u.Journal = selfupdate.JournalFor(primary)
	for _, t := range targets {
		u.Files = append(u.Files, selfupdate.File{Member: executableName(bin), Target: t, Mode: 0o755})
	}
	if bin == "nps" {
		run := common.GetRunPath()
		u.Files = append(u.Files,
			selfupdate.File{Member: "web/views", Target: filepath.Join(run, "web", "views"), Dir: true},
			selfupdate.File{Member: "web/static", Target: filepath.Join(run, "web", "static"), Dir: true},
			selfupdate.File{Member: "conf/nps.conf", Target: filepath.Join(run, "conf", "nps.conf.default"), Mode: 0o644},
		)
	}
	u.Verify = func(ctx context.Context, c *selfupdate.Candidate, paths []string, artifact io.ReaderAt) error {
		// Running the new executable proves it starts on this system, for
		// example that it was built for this CPU variant.
		v, err := executableVersion(paths[0])
		if err != nil {
			return fmt.Errorf("the downloaded %s does not run on this system: %w", executableName(bin), err)
		}
		if version.Compare(v, c.Version) != 0 {
			return fmt.Errorf("the downloaded %s is version %s, want %s", executableName(bin), v, c.Version)
		}
		if bin == "nps" {
			return syncPackageGeoData(ctx, c, artifact)
		}
		return nil
	}

	plan, err := u.Check(ctx)
	if errors.Is(err, selfupdate.ErrPendingRestart) {
		return nil, fmt.Errorf("%w; restart %s to run it", err, bin)
	}
	if err != nil {
		return nil, err
	}
	for _, w := range plan.Warnings {
		fmt.Println("WARNING:", w)
	}
	for _, n := range plan.Route.Notices {
		fmt.Println("NOTICE:", n.Message, n.URL)
	}
	c := plan.Candidate
	if c == nil {
		if plan.Route.Status == selfupdate.RouteHeld {
			return nil, heldError(plan.Route)
		}
		fmt.Println("Already up to date, current version is", version.VERSION)
		return nil, nil
	}
	fmt.Println("Updating to", c.Version)
	running := processesRunning(targets)
	if o.Pending {
		err = u.Apply(ctx, c)
	} else {
		err = u.Install(ctx, c)
	}
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			if common.IsWindows() {
				err = fmt.Errorf("%w; run the update as administrator", err)
			} else {
				err = fmt.Errorf("%w; run the update as root", err)
			}
		}
		return nil, err
	}
	for _, t := range targets {
		fmt.Println("Replaced", t)
	}
	fmt.Println("Update completed:", c.Version)
	return &UpdateResult{Version: c.Version, Replaced: targets, Running: running}, nil
}

// heldError explains why newer releases cannot be installed by an update.
func heldError(r *selfupdate.Route) error {
	msgs := make([]string, 0, len(r.Blocked)+len(r.Unusable))
	for _, reason := range append(slices.Clone(r.Blocked), r.Unusable...) {
		msg := reason.Message
		if msg == "" {
			msg = reason.Code
		}
		if reason.URL != "" {
			msg += " (" + reason.URL + ")"
		}
		msgs = append(msgs, msg)
	}
	return fmt.Errorf("version %s cannot be installed by an update from %s: %s", r.Latest, r.Current, strings.Join(msgs, "; "))
}

// latestRelease returns the release an update of bin would install now, or the
// running version when there is none.
func latestRelease(ctx context.Context, bin string) (string, error) {
	u, err := newUpdater(bin, false)
	if err != nil {
		return "", err
	}
	plan, err := u.Check(ctx)
	if errors.Is(err, selfupdate.ErrPendingRestart) {
		return version.VERSION, nil
	}
	if err != nil {
		return "", err
	}
	if plan.Candidate == nil {
		return version.VERSION, nil
	}
	return plan.Candidate.Version, nil
}

// newUpdater returns an updater of bin without files or journal. An update
// always runs on somebody's request, hence the grants; force reinstalls and
// retries a failed release, but never goes back to an older version.
func newUpdater(bin string, force bool) (*selfupdate.Updater, error) {
	key, err := selfupdate.ParsePublicKey(ReleaseSigningKey)
	if err != nil {
		return nil, fmt.Errorf("this build has no valid release signing key, so it cannot verify updates; download the new release instead: %w", err)
	}
	client := &http.Client{Transport: newHTTPClient()}
	sources := make([]selfupdate.Source, 0, len(manifestURLs))
	for _, url := range manifestURLs {
		sources = append(sources, &selfupdate.HTTPSource{Client: client, URL: url})
	}
	grants := []string{"user", "rollout"}
	if force {
		grants = append(grants, "force", "reinstall", "retry-failed")
	}
	return &selfupdate.Updater{
		Sources: sources,
		Trust:   selfupdate.Trust{Keys: []selfupdate.TrustedKey{{Public: key}}},
		Product: releaseProduct,
		Name:    artifactName(bin),
		Current: version.VERSION,
		Grants:  grants,
	}, nil
}

// artifactName names the release artifact built like this binary of bin.
// It must match the names the release workflow gives the archives of
// build.assets.sh and build.assets.old.sh.
func artifactName(bin string) string {
	if BuildTarget != "" {
		return bin + "-" + BuildTarget
	}
	return bin
}

// syncPackageGeoData installs the geodata files of the release archive when
// they are newer than those in place; see SyncGeoData.
func syncPackageGeoData(ctx context.Context, c *selfupdate.Candidate, artifact io.ReaderAt) error {
	tmp, err := os.MkdirTemp("", "nps-geodata-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	var members []selfupdate.Member
	for _, name := range []string{"geoip.dat", "geosite.dat", geoVersionFile} {
		members = append(members, selfupdate.Member{Name: "conf/" + name, Path: filepath.Join(tmp, name)})
	}
	a := c.Artifact
	if err := selfupdate.Extract(ctx, artifact, a.Size, a.Format, members, selfupdate.ExtractOptions{}); err != nil {
		return fmt.Errorf("geodata of the release: %w", err)
	}
	ipPath, sitePath := geoDataPaths(common.GetRunPath())
	return SyncGeoData(tmp, ipPath, sitePath)
}

// RoundTrip sends req with certificate verification, and again without it
// when the certificate check itself failed, so that devices with a broken
// CA store can still update. Release manifests are signed and artifacts
// checked against them, so the connection only needs to deliver them.
func (c *httpClient) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.verified.Transport.RoundTrip(req)
	if err == nil || !isCertificateError(err) {
		return resp, err
	}
	fmt.Printf("WARNING: certificate verification for %s failed: %v; retrying without it, the signed release manifest still authenticates the download\n", req.URL.Host, err)
	return c.unverified.Transport.RoundTrip(req)
}

var _ http.RoundTripper = (*httpClient)(nil)

// processesRunning returns the other processes whose executable is one of
// paths. It has to run before the executables are replaced: afterwards the
// processes report the moved or deleted original.
func processesRunning(paths []string) []int32 {
	procs, err := process.Processes()
	if err != nil {
		return nil
	}
	self := int32(os.Getpid())
	var pids []int32
	for _, p := range procs {
		if p.Pid == self {
			continue
		}
		if exe, err := p.Exe(); err == nil && exe != "" && containsFile(paths, exe) {
			pids = append(pids, p.Pid)
		}
	}
	return pids
}

// WarnOldProcesses reports the processes among pids that still run, and so
// still run the version an update replaced.
func WarnOldProcesses(pids []int32) {
	var alive []string
	for _, pid := range pids {
		if ok, err := process.PidExists(pid); err == nil && ok {
			alive = append(alive, strconv.Itoa(int(pid)))
		}
	}
	if len(alive) > 0 {
		fmt.Printf("WARNING: processes %s still run the old version; restart them to use the new one\n", strings.Join(alive, ", "))
	}
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

// executableTargets returns the running executable, on Unix the copies
// install placed in the system bin directories, and the executable the
// installed service runs.
func executableTargets(bin, service string) ([]string, error) {
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
	if p := serviceExecutable(service); p != "" && isExecutableOf(p, bin) && !containsFile(targets, p) {
		targets = append(targets, p)
	}
	return targets, nil
}

// isExecutableOf reports whether p is an existing file named like the
// executable of bin.
func isExecutableOf(p, bin string) bool {
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	name := filepath.Base(p)
	if common.IsWindows() {
		return strings.EqualFold(name, executableName(bin))
	}
	return name == bin
}
