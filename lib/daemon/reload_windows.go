package daemon

// WatchReload does nothing: Windows has no reload signal.
func WatchReload(func()) {}
