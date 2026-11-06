package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/upstream"
	"github.com/djylb/upstream/health"
	"github.com/djylb/upstream/uphttp"
)

// healthKey identifies one checked address of one health check section;
// sections may check the same address with different settings.
type healthKey struct {
	h    *file.Health
	addr string
}

// HealthChecker checks the targets of the configured health sections and
// reports to the server when a target goes down or comes back up.
type HealthChecker struct {
	checker    *health.Checker[healthKey, struct{}]
	serverConn *conn.Conn
	// fails counts the failing checks of each target that is down; it is
	// only used from the checker's callback goroutine.
	fails map[healthKey]int
}

func NewHealthChecker(parentCtx context.Context, healths []*file.Health, c *conn.Conn) *HealthChecker {
	hc := &HealthChecker{serverConn: c, fails: make(map[healthKey]int)}
	hc.checker = health.New[healthKey, struct{}](parentCtx, health.Config[healthKey]{
		OnResult: hc.onResult,
		OnRemove: func(k healthKey, _ uint64) { delete(hc.fails, k) },
	})
	for _, h := range healths {
		if !healthConfigValid(h) {
			continue
		}
		t := health.Target{Probe: healthProbe(h), Options: health.Options{
			Interval: time.Duration(h.HealthCheckInterval) * time.Second,
			Timeout:  time.Duration(h.HealthCheckTimeout) * time.Second,
			Rise:     1,
			Fall:     h.HealthMaxFail,
		}}
		for _, addr := range strings.Split(h.HealthCheckTarget, ",") {
			t.Address = addr
			hc.checker.Put(healthKey{h: h, addr: addr}, t)
		}
	}
	return hc
}

// healthConfigValid reports whether h can be scheduled: a zero fail count,
// interval or timeout would never mark a target down or never time out.
func healthConfigValid(h *file.Health) bool {
	return h != nil && h.HealthMaxFail > 0 && h.HealthCheckInterval > 0 && h.HealthCheckTimeout > 0
}

// healthProbe returns the check for h's health_check_type.
func healthProbe(h *file.Health) health.Probe {
	switch h.HealthCheckType {
	case "tcp":
		return health.TCP(health.TCPOptions{})
	case "http", "https":
		return uphttp.Probe(uphttp.Check{
			Scheme: h.HealthCheckType,
			Path:   h.HttpHealthUrl,
			Status: uphttp.StatusRange{Min: 200, Max: 200},
		})
	default:
		err := fmt.Errorf("unsupported health check type: %s", h.HealthCheckType)
		return func(context.Context, health.Target) (health.Observation, error) {
			return health.Observation{}, err
		}
	}
}

// onResult reports a target down when it fails health_check_max_failed
// times in a row, and again after every further health_check_max_failed
// failures, and reports it up on its first passing check after that.
func (hc *HealthChecker) onResult(r health.Report[healthKey]) {
	switch r.Health {
	case upstream.HealthDown:
		if r.Changed() {
			hc.fails[r.Key] = 0
		}
		hc.fails[r.Key]++
		if (hc.fails[r.Key]-1)%r.Key.h.HealthMaxFail == 0 {
			hc.send(r.Key.addr, "0")
		}
	case upstream.HealthUp:
		if r.Previous == upstream.HealthDown {
			hc.send(r.Key.addr, "1")
		}
		delete(hc.fails, r.Key)
	case upstream.HealthUnknown:
		// No verdict yet: nothing to report.
	}
}

func (hc *HealthChecker) send(addr, status string) {
	if hc.serverConn == nil {
		return
	}
	if err := legacy.WriteHealth(hc.serverConn, addr, status); err != nil {
		logs.Trace("send health info of %s: %v", addr, err)
	}
}

// Stop ends all checks and waits until no check or report is running.
func (hc *HealthChecker) Stop() {
	hc.checker.Close()
	<-hc.checker.Done()
}
