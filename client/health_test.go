package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/upstream"
	"github.com/djylb/upstream/health"
)

// readHealth decodes health reports on the server side of a pipe as
// "group/addr=0" or "group/addr=1".
func readHealth(t *testing.T, c net.Conn) <-chan string {
	t.Helper()
	ch := make(chan string, 16)
	go func() {
		defer close(ch)
		sc := conn.NewConn(c)
		for {
			rep, err := legacy.ReadHealth(sc)
			if err != nil {
				return
			}
			status := "=0"
			if rep.Up {
				status = "=1"
			}
			ch <- rep.Group + "/" + rep.Addr + status
		}
	}()
	return ch
}

func TestHealthCheckerReportCadence(t *testing.T) {
	sideA, sideB := net.Pipe()
	defer func() { _ = sideB.Close() }()
	reports := readHealth(t, sideB)
	hc := NewHealthChecker(context.Background(), legacyHealthSink{signal: conn.NewConn(sideA)})
	defer hc.Stop()
	hc.setGroup(healthcheck.Group{Name: "t1", Type: "invalid", Interval: 3600, Timeout: 1, Fall: 2, Targets: []string{"a:1"}})
	want := func(s string) {
		t.Helper()
		select {
		case got := <-reports:
			if got != s {
				t.Fatalf("report %q, want %q", got, s)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no report %q", s)
		}
	}
	want("t1/a:1=0") // the first failure decides
	key := hc.checker.GroupKeys("t1")[0]
	report := func(prev, cur upstream.Health) {
		r := health.Report[healthKey]{Key: key}
		r.Target.Options.Fall = 2
		r.Previous, r.Health = prev, cur
		hc.onResult(r)
	}
	report(upstream.HealthDown, upstream.HealthDown) // second failure: nothing
	report(upstream.HealthDown, upstream.HealthDown)
	want("t1/a:1=0")
	report(upstream.HealthDown, upstream.HealthUp)
	want("t1/a:1=1")
	report(upstream.HealthUp, upstream.HealthUp) // nothing
	select {
	case got := <-reports:
		t.Fatalf("unexpected report %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}
