package httpproxy

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"
)

// Every request that ends reaches AccessLog: forwarded ones with their
// target, reused backend connections included, and refused ones.
func TestAccessLogRecordsEveryRequest(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	var mu sync.Mutex
	var got []AccessRecord
	e.hp.AccessLog = func(r AccessRecord) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	}
	b := newBackend(t, "a", false)
	h := e.addHost("plain.test", b.addr, nil)
	e.addHost("auth.test", b.addr, func(h *model.Host) { h.UserAuth = &model.MultiAccount{AccountMap: map[string]string{"u": "p"}} })

	for range 2 {
		if r := e.mustGet("plain.test", "/p?q=1", http.Header{"User-Agent": {"ua"}}); r.StatusCode != 200 {
			t.Fatalf("status %d", r.StatusCode)
		}
	}
	if r := e.mustGet("missing.test", "/x", nil); r.StatusCode != 404 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if r := e.mustGet("auth.test", "/", nil); r.StatusCode != 401 {
		t.Fatalf("status %d", r.StatusCode)
	}

	// a record is written after its response was sent
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		if len(got) >= 4 || time.Now().After(deadline) {
			break
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	defer mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("%d records: %+v", len(got), got)
	}
	for _, r := range got[:2] {
		if r.Route != "plain.test" || r.Host != h.Id || r.Status != 200 || r.Target != b.addr ||
			r.Path != "/p" || r.Query != "q=1" || r.UserAgent != "ua" || r.Domain != "plain.test" || r.SrcIP != "127.0.0.1" {
			t.Fatalf("record %+v", r)
		}
	}
	if r := got[2]; r.Route != "http" || r.Host != 0 || r.Status != 404 || r.Domain != "missing.test" {
		t.Fatalf("not found record %+v", r)
	}
	if r := got[3]; r.Route != "auth.test" || r.Status != 401 || r.Target != "" {
		t.Fatalf("auth record %+v", r)
	}
}
