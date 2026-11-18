package runenv

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"strconv"
	"strings"
)

// totalMemory returns the physical memory, or the memory limit of the
// process's cgroup (v2) when that is lower; 0 if unknown.
func totalMemory() int64 {
	total := memTotal()
	if l := cgroupLimit(); l > 0 && (total == 0 || l < total) {
		total = l
	}
	return total
}

func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "MemTotal:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}

// cgroupLimit returns the lowest memory.max from the process's cgroup v2
// up to the root, 0 if none.
func cgroupLimit() int64 {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0
	}
	var dir string
	for _, line := range bytes.Split(b, []byte("\n")) {
		if p, ok := bytes.CutPrefix(line, []byte("0::")); ok {
			dir = string(p)
		}
	}
	if dir == "" {
		return 0
	}
	var low int64
	for d := path.Clean("/" + dir); ; d = path.Dir(d) {
		if v, err := os.ReadFile("/sys/fs/cgroup" + d + "/memory.max"); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64); err == nil && (low == 0 || n < low) {
				low = n
			}
		}
		if d == "/" {
			return low
		}
	}
}
