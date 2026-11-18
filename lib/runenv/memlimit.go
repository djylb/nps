package runenv

import (
	"os"
	"runtime/debug"
)

// smallMemory is the total memory under which a process is on a small
// device, shared with the system and other services.
const smallMemory = 1 << 30

// SetSoftMemoryLimit gives the Go runtime a soft memory limit of half the
// total memory on a small device, so that the collector works harder before
// the system runs out of memory; the limit fails no allocation. GOMEMLIMIT,
// when set, wins. It returns the limit set, 0 for none.
func SetSoftMemoryLimit() int64 {
	if os.Getenv("GOMEMLIMIT") != "" {
		return 0
	}
	total := totalMemory()
	if total <= 0 || total >= smallMemory {
		return 0
	}
	debug.SetMemoryLimit(total / 2)
	return total / 2
}
