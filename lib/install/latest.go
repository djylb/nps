package install

import (
	"sync"
	"time"

	"github.com/djylb/nps/lib/version"
)

const (
	latestTTL   = 6 * time.Hour
	latestRetry = 10 * time.Minute
)

// latest caches the tag of the latest release, so that pages can tell
// whether an update exists without waiting for GitHub.
var latest struct {
	sync.Mutex
	tag      string
	next     time.Time // when to query again
	fetching bool
}

// fetchLatest queries the latest release tag; tests replace it.
var fetchLatest = func() (string, error) {
	rl, _, err := fetchLatestRelease(newHTTPClient(), "djylb/nps")
	if err != nil {
		return "", err
	}
	return rl.TagName, nil
}

// LatestVersion returns the tag of the latest release as last queried, or ""
// before a query succeeded. A stale or missing tag is queried again in the
// background, every 6 hours, or 10 minutes after a failure.
func LatestVersion() string {
	latest.Lock()
	defer latest.Unlock()
	if !latest.fetching && !time.Now().Before(latest.next) {
		latest.fetching = true
		go refreshLatest()
	}
	return latest.tag
}

func refreshLatest() {
	tag, err := fetchLatest()
	latest.Lock()
	defer latest.Unlock()
	latest.fetching = false
	if err != nil {
		latest.next = time.Now().Add(latestRetry)
		return
	}
	latest.tag = tag
	latest.next = time.Now().Add(latestTTL)
}

// UpdateAvailable reports whether the latest release known is newer than
// this build.
func UpdateAvailable() bool {
	tag := LatestVersion()
	return tag != "" && version.Compare(tag, version.VERSION) > 0
}
