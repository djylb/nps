package version

const VERSION = "0.29.38.1"

var MinVersions = []string{
	"0.26.0", // 0
	"0.27.0", // 1
	"0.28.0", // 2
	"0.29.0", // 3
}

func GetVersion(index int) string {
	if index < 0 || index >= len(MinVersions) {
		return GetLatest()
	}
	return MinVersions[index]
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
