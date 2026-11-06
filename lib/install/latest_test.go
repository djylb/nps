package install

import (
	"errors"
	"testing"
	"time"

	"github.com/djylb/nps/lib/version"
)

func waitFetched(t *testing.T) {
	t.Helper()
	for range 200 {
		latest.Lock()
		done := !latest.fetching
		latest.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("latest release query did not finish")
}

func TestUpdateAvailable(t *testing.T) {
	orig := fetchLatest
	t.Cleanup(func() {
		fetchLatest = orig
		latest.Lock()
		latest.tag, latest.next = "", time.Time{}
		latest.Unlock()
	})
	reset := func(tag string, err error) {
		fetchLatest = func() (string, error) { return tag, err }
		latest.Lock()
		latest.tag, latest.next = "", time.Time{}
		latest.Unlock()
	}

	reset("v99.0.0", nil)
	if UpdateAvailable() {
		t.Fatal("an update reported before the query finished")
	}
	waitFetched(t)
	if !UpdateAvailable() || LatestVersion() != "v99.0.0" {
		t.Fatalf("newer release not reported: %q", LatestVersion())
	}

	reset("v"+version.VERSION, nil)
	UpdateAvailable()
	waitFetched(t)
	if UpdateAvailable() {
		t.Fatal("the running version reported as an update")
	}

	reset("", errors.New("offline"))
	UpdateAvailable()
	waitFetched(t)
	if UpdateAvailable() {
		t.Fatal("a failed query reported an update")
	}
}
