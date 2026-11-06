package version

import "testing"

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v0.34.7", "0.34.8", -1},
		{"v0.34.10", "0.34.9", 1},
		{"v0.34.8", "0.34.8", 0},
		{"0.35", "0.34.99", 1},
		{"0.34.8", "0.34.8.0", 0},
		{"0.34.9-beta.1", "0.34.9", -1},
		{"0.34.9-rc.1", "0.34.8", 1},
		{"0.34.9-beta", "0.34.9-rc", -1},
		{"0.34.9+build.5", "0.34.9", 0},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// A version scheme that stops counting up reads as older and is not sent an
// update request, which only an old npc would misread.
