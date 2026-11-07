// Package textutil parses loosely written configuration values and lists
// and normalizes line endings.
package textutil

import (
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
)

// ParseBool parses a boolean config value the way strconv.ParseBool does,
// ignoring surrounding space: 1, t, T, TRUE, true, True and their false
// counterparts. Any other value yields false and an error for the caller to
// report.
func ParseBool(s string) (bool, error) {
	return strconv.ParseBool(strings.TrimSpace(s))
}

// GetIntNoErrByStr int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// GetTimeNoErrByStr time
func GetTimeNoErrByStr(str string) time.Time {
	str = strings.TrimSpace(str)
	if str == "" {
		return time.Time{}
	}
	if timestamp, err := strconv.ParseInt(str, 10, 64); err == nil {
		if timestamp > 1_000_000_000_000 {
			return time.UnixMilli(timestamp)
		}
		return time.Unix(timestamp, 0)
	}
	t, err := dateparse.ParseLocal(str)
	if err == nil {
		return t
	}
	return time.Time{}
}

func ContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func HandleArrEmptyVal(list []string) []string {
	for len(list) > 0 && (list[len(list)-1] == "" || strings.TrimSpace(list[len(list)-1]) == "") {
		list = list[:len(list)-1]
	}
	for i := 0; i < len(list); i++ {
		list[i] = strings.TrimSpace(list[i])
		if i > 0 && list[i] == "" {
			list[i] = list[i-1]
		}
	}
	return list
}

func ExtendArrs(arrays ...*[]string) int {
	maxLength := 0
	for _, arr := range arrays {
		if len(*arr) > maxLength {
			maxLength = len(*arr)
		}
	}
	if maxLength == 0 {
		return 0
	}
	for _, arr := range arrays {
		for len(*arr) < maxLength {
			if len(*arr) == 0 {
				*arr = append(*arr, "")
			} else {
				*arr = append(*arr, (*arr)[len(*arr)-1])
			}
		}
	}
	return maxLength
}
