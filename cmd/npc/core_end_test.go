//go:build !sdk

package main

import (
	"context"
	"errors"
	"testing"
)

func TestCoreProfileEnd(t *testing.T) {
	live := context.Background()
	stopped, cancel := context.WithCancel(live)
	cancel()
	bad := errors.New("bad server")
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		auto bool
		fail bool
	}{
		{"ended, no reconnect", live, nil, false, true},
		{"invalid server", live, bad, true, true},
		{"invalid server, no reconnect", live, bad, false, true},
		{"stopped", stopped, nil, false, false},
		{"stopped with error", stopped, bad, true, false},
	} {
		if got := coreProfileEnd(tc.ctx, tc.err, tc.auto); (got != nil) != tc.fail {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}
