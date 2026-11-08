package session

import (
	"testing"

	"github.com/djylb/nps/internal/coreproto/coreapi"
)

const testBundle = `{"kinds":{"default":[{}]}}`

func TestShapingOnlyWhenSwitchAndSource(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sw, source, want bool
	}{{"both", true, true, true}, {"switch off", false, true, false}, {"no source", true, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := start(t, "tcp", coreapi.ClientRecord{ID: 1, VKey: "key-1", Enabled: true, Crypt: true, Shaping: tc.sw})
			if tc.source {
				e.a.SetShaping([]byte(testBundle))
			}
			cs := e.dial(e.dialer("key-1"))
			e.waitOnline()
			waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
			cs.mu.Lock()
			have := len(cs.shapingRaw) > 0
			cs.mu.Unlock()
			if have != tc.want {
				t.Fatalf("shaping present=%v, want %v", have, tc.want)
			}
		})
	}
}
