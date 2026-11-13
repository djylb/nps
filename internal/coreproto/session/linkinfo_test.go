package session

import "testing"

// The server session reports the client's address, its record mode and the
// carriers of its pipes as they come.
func TestSessionLinkInfo(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	cs := e.dial(e.multi("key", "tcp://"+e.addr+"?tier=0|tcp://"+e.addr+"?tier=0&standby=1&weight=2"))
	ss := e.waitOnline()
	if got := ss.LocalAddr(); got != "127.0.0.1" {
		t.Fatalf("LocalAddr = %q", got)
	}
	if got := ss.Record(); got != "sealed" && got != "bound" {
		t.Fatalf("Record = %q", got)
	}
	waitUntil(t, "two pipes", func() bool { return len(cs.zc.Pipes()) == 2 && len(ss.Pipes()) == 2 })
	if got := ss.Pipes(); got[0] != "tcp" || got[1] != "tcp" {
		t.Fatalf("Pipes = %q", got)
	}
}
