package httpproxy

import (
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestPlansKeepNoPlanCompiledAcrossARemove(t *testing.T) {
	c := NewPlans()
	s := &HttpServer{HttpProxy: &HttpProxy{}}
	h := &model.Host{Id: 1}
	compiled := 0
	compile := func(h *model.Host) *hostPlan {
		compiled++
		p := s.compilePlan(h)
		if compiled == 1 {
			c.Remove(h.Id) // an edit lands while the first plan compiles
		}
		return p
	}
	first := c.get(h, compile)
	if second := c.get(h, compile); second == first || compiled != 2 {
		t.Fatalf("plan compiled before the edit was kept (%d compilations)", compiled)
	}
	if third := c.get(h, compile); compiled != 2 || third == first {
		t.Fatalf("plan compiled after the edit was not kept (%d compilations)", compiled)
	}
	// Another entry with the same id gets a plan of its own.
	other := &model.Host{Id: 1}
	if p := c.get(other, compile); p.host != other {
		t.Fatal("plan of a replaced entry served")
	}
}

// Requests racing with edits read the settings only through the plan.
func TestHostEditsRaceWithRequests(t *testing.T) {
	e := newProxyEnv(t, envOptions{})
	b := newBackend(t, "a", false)
	h := e.addHost("race.test", b.addr, nil)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for range 10 {
				r, err := e.get("race.test", "/", nil)
				if err != nil || r.StatusCode != http.StatusOK {
					t.Errorf("request %d: %v %v", i, r.Response, err)
					return
				}
			}
		})
	}
	for i := range 20 {
		e.editHost(h, func(h *model.Host) { h.HeaderChange = "X-Test: v" + strconv.Itoa(i) })
	}
	wg.Wait()
	if got := e.mustGet("race.test", "/", nil).info(t)["xtest"]; got != "v19" {
		t.Fatalf("X-Test = %q after the last edit", got)
	}
}
