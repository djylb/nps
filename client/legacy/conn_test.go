package legacyclient

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/connprofile"
)

func TestNewConnReturnsDialError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	_, _, err = NewConn("tcp", "key", addr, "", "")
	if err == nil || strings.Contains(err.Error(), "unexpected nil connection") {
		t.Fatalf("NewConn error = %v, want the dial error", err)
	}
}

// dialServer tries the candidates of a profile in connprofile.Order, the
// last one that connected first, and tells of each that fails.
func TestDialServerTriesCandidatesInOrder(t *testing.T) {
	var ports []string
	for range 3 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, l.Addr().String())
		_ = l.Close()
	}
	server := "tcp://" + ports[0] + "?standby=1|" + ports[1] + "|tcp://" + ports[2] + "?tier=-1|tcp://" + ports[2] + "?tier=-9&dirs=up"
	var tried []string
	failed := func(c connprofile.Candidate, err error) { tried = append(tried, c.Addr+"/"+c.Carrier) }
	_, _, _, err := dialServer("tcp", "key", server, "", "", failed)
	if err == nil {
		t.Fatal("no error")
	}
	want := []string{ports[2] + "/tcp", ports[1] + "/tcp", ports[0] + "/tcp"}
	if !slices.Equal(tried, want) {
		t.Fatalf("tried %v, want %v", tried, want)
	}
	lastCandidate.Store("tcp "+server, ports[1])
	defer lastCandidate.Delete("tcp " + server)
	tried = nil
	_, _, _, _ = dialServer("tcp", "key", server, "", "", failed)
	if want = []string{ports[1] + "/tcp", ports[2] + "/tcp", ports[0] + "/tcp"}; !slices.Equal(tried, want) {
		t.Fatalf("after a success tried %v, want %v", tried, want)
	}
	if _, _, _, err := dialServer("tcp", "key", "ftp://x", "", "", nil); err == nil {
		t.Fatal("bad profile accepted")
	}
}
