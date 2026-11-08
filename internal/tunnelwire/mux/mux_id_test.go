package mux

import "testing"

// The two sides of a mux open streams with ids of different parity, so that
// streams both open at once never share an id.
func TestStreamIDParity(t *testing.T) {
	for _, tc := range []struct {
		initiator bool
		parity    int32
	}{{true, 1}, {false, 0}} {
		m := &Mux{connMap: NewConnMap(), isInitiator: tc.initiator}
		if tc.initiator {
			m.id = -1
		}
		for range 3 {
			if id := m.getId(); id%2 != tc.parity {
				t.Fatalf("initiator=%t: id %d", tc.initiator, id)
			}
		}
	}
}
