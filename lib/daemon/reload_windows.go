package daemon

import "errors"

// WatchReload does nothing: Windows has no reload signal.
func WatchReload(func()) {}

func signalReload(int) error {
	return errors.New("reload signal is not supported on windows")
}
