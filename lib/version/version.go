package version

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

const VERSION = "0.34.8"
const MinVer = 7

var MinVersions = []string{
	"0.26.0", // 0
	"0.27.0", // 1
	"0.28.0", // 2
	"0.29.0", // 3
	"0.30.0", // 4
	"0.31.0", // 5
	"0.32.0", // 6
	"0.33.0", // 7
	"0.34.0", // 8
}

func GetVersion(index int) string {
	if index < 0 || index >= len(MinVersions) {
		return GetLatest()
	}
	return MinVersions[index]
}

func GetMinVersion(SecureMode bool) string {
	if SecureMode {
		return GetVersion(MinVer)
	}
	return GetVersion(0)
}

func GetCount() int {
	return len(MinVersions)
}

func GetLatest() string {
	if len(MinVersions) == 0 {
		return ""
	}
	return MinVersions[len(MinVersions)-1]
}

func GetIndex(ver string) int {
	for i, v := range MinVersions {
		if v == ver {
			return i
		}
	}
	return -1
}

func GetLatestIndex() int {
	if GetCount() == 0 {
		return 0
	}
	return GetCount() - 1
}

func PrintVersion(ver int) {
	fmt.Printf("Version: %s\nCore version: %s\nSame core version of client and server can connect each other\n", VERSION, GetVersion(ver))
}

// P2PMinVersion is the oldest npc release that speaks the current P2P
// protocol. Older clients are refused with a log entry naming this version.
const P2PMinVersion = "0.34.8"

// P2PMinIndex is the lowest protocol index a P2P client may use: the provider
// opens its P2P control streams on the tunnel, which the bridge accepts from
// that index on.
const P2PMinIndex = 5

// SupportsP2P reports whether a client announcing clientVersion with protocol
// index ver can take part in a P2P session.
func SupportsP2P(ver int, clientVersion string) bool {
	return ver >= P2PMinIndex && Compare(clientVersion, P2PMinVersion) >= 0
}

// Compare compares two dotted versions numerically segment by segment.
// A leading "v" and build metadata are ignored; a pre-release suffix sorts
// before the release with the same numbers.
func Compare(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)
	for i := 0; i < max(len(an), len(bn)); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			return cmp.Compare(x, y)
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i], _ = strconv.Atoi(p)
	}
	return nums, pre
}
