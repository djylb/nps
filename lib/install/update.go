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

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"

	"github.com/djylb/nps/lib/runenv"
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
// old system, such as "v7" or "softfloat"; set by scripts/build_assets.sh.
var BuildTarget string

// releaseProduct is the product named in the release manifests.
const releaseProduct = "nps"

// All versions read one update manifest at a fixed location, which lists the
// releases of every major version; its rules decide what may cross a major
// version. See scripts/publish_manifest.sh for where it is published.
const (
	githubReleases = "https://github.com/djylb/nps/releases/download/"
	mirrorPackage  = "nps-mirror@"
)

// cdnRepos are the mirrors of the repositories on GitHub, in the order tried.
var cdnRepos = []string{
	"https://cdn.jsdelivr.net/gh/djylb/",
	"https://fastly.jsdelivr.net/gh/djylb/",
	"https://gcore.jsdelivr.net/gh/djylb/",
	"https://testingcf.jsdelivr.net/gh/djylb/",
}

// mirrorSource reads artifacts, listed by their release URL, from the same tag
// of the mirror repository. The manifest comes from a branch instead: files of a tag
// are cached for good, so a renewed manifest would never reach the clients.
type mirrorSource struct {
	selfupdate.HTTPSource
}

// Open implements selfupdate.Source.
func (s *mirrorSource) Open(ctx context.Context, a selfupdate.Artifact, offset int64) (io.ReadCloser, int64, error) {
	if rest, ok := strings.CutPrefix(a.URL, githubReleases); ok {
		a.URL = mirrorPackage + rest
	}
	return s.HTTPSource.Open(ctx, a, offset)
}

// updateSources returns the sources of the update manifest, in the order tried.
func updateSources(client *http.Client) []selfupdate.Source {
	sources := []selfupdate.Source{&selfupdate.HTTPSource{
		Client: client,
		URL:    githubReleases + "channel/manifest.json",
	}}
	for _, repo := range cdnRepos {
		sources = append(sources, &mirrorSource{selfupdate.HTTPSource{
			Client: client,
			URL:    repo + "nps@manifest-channel/manifest.json",
			Base:   repo,
		}})
	}
	return sources
}

// UpdateOptions selects how Update installs a release.
type UpdateOptions struct {
	// Force installs the latest release whatever the running version is:
	// again when it is that release, back when it is newer, and also when
	// that release failed before. It lifts no gate of the manifest without
	// an unlock, so it stays within what the rules allow.
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
	// Services are the services other than the one of the calling process
	// that ran a replaced executable, to be restarted by RestartAfterUpdate.
	Services []string
}

// Update replaces the executables of bin ("nps" or "npc") with the latest
// release: the running one and, when the calling process runs as a service
// whose executable is another file, that file (see updateTargets). Other
// copies and other instances are left alone: each one updates itself, so its
// update journal stays next to it. For nps the web assets and the reference
// config of the run directory are replaced along with them. All files are
// replaced as one set, so an interrupted update is undone on the next start.
// A nil result with a nil error means the running version is already the
// latest. service is the default service name of bin.
//
// Update fetches the signed manifest, picks the release, verifies and replaces
// the files; selfupdate does each of these, Update only chooses the files and
// checks that the new executable runs. Restarting is left to the caller:
// RestartAfterUpdate for the services, or UpdateSelf for the running process.
func Update(ctx context.Context, bin, service string, o UpdateOptions) (_ *UpdateResult, err error) {
	var to string
	defer func() {
		if err != nil {
			slogx.Emit(context.Background(), log, &logevent.UpdateFailed, "from", version.VERSION, "to", to, "reason", "update_failed", "err", err)
		}
	}()
	targets, err := updateTargets(bin, service)
	if err != nil {
		return nil, err
	}
	u, err := newUpdater(bin, o.Force)
	if err != nil {
		return nil, err
	}
	// The journal sits next to the running executable: this process, or
	// the one it restarts as, confirms or rolls back a pending update, and
	// looks for the journal next to itself (StartUpdate).
	u.Executable = targets[0]
	u.Journal = selfupdate.JournalFor(targets[0])
	u.Files = updateFiles(bin, targets)
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
	to = c.Version
	slogx.Emit(context.Background(), log, &logevent.UpdateAvailable, "from", version.VERSION, "to", c.Version)
	running := processesRunning(targets)
	services := servicesToRestart(service, targets, running)
	if p := serviceExecutable(service); p != "" && !containsFile(targets, p) && isExecutableOf(p, bin) && !runsVersion(p, c.Version) {
		fmt.Printf("NOTE: service %s runs %s, which this update leaves alone; run \"%s update\" to update it\n", service, p, p)
	}
	if o.Pending {
		err = u.Apply(ctx, c)
	} else {
		err = u.Install(ctx, c)
	}
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			err = fmt.Errorf("%w; %s", err, permissionHint)
		}
		return nil, err
	}
	for _, t := range targets {
		fmt.Println("Replaced", t)
	}
	slogx.Emit(context.Background(), log, &logevent.UpdateApplied, "from", version.VERSION, "to", c.Version)
	return &UpdateResult{Version: c.Version, Replaced: targets, Running: running, Services: services}, nil
}

// updateFiles lists the files an update of bin replaces as one set: the
// executable at each of targets and, for nps, the web assets and the reference
// config of the run directory.
func updateFiles(bin string, targets []string) []selfupdate.File {
	files := make([]selfupdate.File, 0, len(targets)+3)
	for _, t := range targets {
		files = append(files, selfupdate.File{Member: executableName(bin), Target: t, Mode: 0o755})
	}
	if bin == "nps" {
		run := runenv.GetRunPath()
		files = append(files,
			selfupdate.File{Member: "web/views", Target: filepath.Join(run, "web", "views"), Dir: true},
			selfupdate.File{Member: "web/static", Target: filepath.Join(run, "web", "static"), Dir: true},
			selfupdate.File{Member: "conf/nps.conf", Target: filepath.Join(run, "conf", "nps.conf.default"), Mode: 0o644},
		)
	}
	return files
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
// always runs on somebody's request, hence the grants; force installs the
// latest release even if it is not newer or failed before. Tests replace it.
var newUpdater = releaseUpdater

func releaseUpdater(bin string, force bool) (*selfupdate.Updater, error) {
	key, err := selfupdate.ParsePublicKey(ReleaseSigningKey)
	if err != nil {
		return nil, fmt.Errorf("this build has no valid release signing key, so it cannot verify updates; download the new release instead: %w", err)
	}
	grants := []string{"user", "rollout"}
	if force {
		grants = append(grants, "force", "reinstall", selfupdate.GrantDowngradeAny, "retry-failed")
	}
	return &selfupdate.Updater{
		Sources: updateSources(&http.Client{Transport: newHTTPClient()}),
		Trust:   selfupdate.Trust{Keys: []selfupdate.TrustedKey{{Public: key}}},
		Product: releaseProduct,
		Name:    artifactName(bin),
		Current: version.VERSION,
		Grants:  grants,
	}, nil
}

// artifactName names the release artifact built like this binary of bin.
// It must match the names the release workflow gives the archives of
// scripts/build_assets.sh and scripts/build_assets_old.sh.
func artifactName(bin string) string {
	if BuildTarget != "" {
		return bin + "-" + BuildTarget
	}
	return bin
}

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
	for line := range strings.SplitSeq(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Version:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", errors.New("no version in output")
}

// updateTargets is executableTargets; tests replace it.
var updateTargets = executableTargets

// executableTargets returns the executables an update of the calling
// process replaces, the running one first; see selectTargets.
func executableTargets(bin, _ string) ([]string, error) {
	exe, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	own := serviceOfProcess(os.Getpid())
	ownExe := ""
	if own != "" {
		ownExe = serviceExecutable(own)
	}
	return selectTargets(bin, exe, ownExe), nil
}

// selectTargets returns the running executable exe and, when the calling
// process runs as a service whose configured executable ownExe is another
// file (a link resolved differently, or a copy the service manager starts),
// that file as well, so the service starts the new version too. Nothing
// else is replaced: copies in the system bin directories and the
// executables of other services, the default one included, belong to other
// instances, which update themselves. From the command line, outside a
// service, that leaves exe alone; the default service is updated with it
// exactly when it runs exe.
func selectTargets(bin, exe, ownExe string) []string {
	targets := []string{exe}
	if ownExe != "" && !containsFile(targets, ownExe) && isOwnExecutable(ownExe, exe, bin) {
		targets = append(targets, ownExe)
	}
	return targets
}

// isOwnExecutable reports whether p, the executable of the service of the
// calling process, is a file that an update of exe may replace: a regular
// file named like exe or like the executable of bin, not, say, a shell the
// service runs exe with.
func isOwnExecutable(p, exe, bin string) bool {
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	name := filepath.Base(p)
	return name == filepath.Base(exe) || isExecutableName(name, bin)
}

// isExecutableOf reports whether p is an existing file named like the
// executable of bin.
func isExecutableOf(p, bin string) bool {
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return isExecutableName(filepath.Base(p), bin)
}

// servicesToRestart returns the services that run one of the processes
// running (which run a replaced executable), and the default service when
// the executable it runs is one of targets, in that order and without the
// service of the calling process, which restarts itself.
func servicesToRestart(defaultService string, targets []string, running []int32) []string {
	own := serviceOfProcess(os.Getpid())
	var services []string
	add := func(name string) {
		if name != "" && name != own && !slices.Contains(services, name) {
			services = append(services, name)
		}
	}
	for _, pid := range running {
		add(serviceOfProcess(int(pid)))
	}
	if defaultService != "" {
		// Where this package cannot ask the service manager for the
		// executable, the default service is assumed to run the updated
		// one, as install sets it up.
		p := serviceExecutable(defaultService)
		if (p == "" && !locatesServices()) || (p != "" && containsFile(targets, p)) {
			add(defaultService)
		}
	}
	return services
}

// runsVersion reports whether the executable at path is version v; an
// executable whose version cannot be read is taken as another one.
func runsVersion(path, v string) bool {
	got, err := executableVersion(path)
	return err == nil && version.Compare(got, v) == 0
}
