//go:build !linux

package runenv

// totalMemory is known on Linux only, where the small devices are.
func totalMemory() int64 { return 0 }
