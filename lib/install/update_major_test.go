package install

import "testing"

func TestCheckMajor(t *testing.T) {
	for _, c := range []struct {
		current, target string
		ok              bool
	}{
		{"0.35.0", "0.35.1", true},
		{"0.35.0", "0.99.0", true},
		{"0.35.0", "v0.36.0", true},
		{"1.2.0", "1.9.0", true},
		{"0.99.0", "1.0.0", false},
		{"0.35.0", "v1.0.0", false},
		{"1.0.0", "0.99.0", false},
		{"0.35.0", "10.0.0", false},
	} {
		if err := checkMajor(c.current, c.target); (err == nil) != c.ok {
			t.Errorf("checkMajor(%q, %q) = %v, want ok=%v", c.current, c.target, err, c.ok)
		}
	}
}
