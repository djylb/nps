package main

import "testing"

func TestCommandLineMode(t *testing.T) {
	for _, tc := range []struct {
		server, vkey string
		cmd, err     bool
	}{
		{"a:1", "k", true, false},
		{"", "", false, false},
		{"", "k", false, true},
		{"a:1", "", false, true},
	} {
		cmd, err := commandLineMode(tc.server, tc.vkey)
		if cmd != tc.cmd || (err != nil) != tc.err {
			t.Errorf("commandLineMode(%q, %q) = %v, %v", tc.server, tc.vkey, cmd, err)
		}
	}
}
