package common

import "strings"

// NormalizeNewlines turns the line endings of s, \r\n (Windows), \r (classic
// Mac) and \n (Unix), into \n, and drops a byte order mark at the start.
func NormalizeNewlines(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	if !strings.Contains(s, "\r") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// SplitLines splits s into lines ending in \r\n, \r or \n.
func SplitLines(s string) []string {
	return strings.Split(NormalizeNewlines(s), "\n")
}
