package version

import "fmt"

const VERSION = "0.34.0"
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
