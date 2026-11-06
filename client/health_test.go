package client

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/upstream"
	"github.com/djylb/upstream/health"
)

// readHealth decodes health reports on the server side of a pipe as
// "addr=0" or "addr=1".
func readHealth(t *testing.T, c net.Conn) <-chan string {
	t.Helper()
	ch := make(chan string, 16)
	go func() {
		defer close(ch)
		sc := conn.NewConn(c)
		for {
			info, status, err := legacy.ReadHealth(sc)
			if err != nil {
				return
			}
			if status {
				ch <- info + "=1"
			} else {
				ch <- info + "=0"
			}
		}
	}()
	return ch
}

func TestHealthCheckerReportsFailingTarget(t *testing.T) {
	sideA, sideB := net.Pipe()
	defer func() { _ = sideB.Close() }()
	reports := readHealth(t, sideB)

	healths := []*file.Health{
		{HealthCheckTimeout: 1, HealthMaxFail: 1, HealthCheckInterval: 1, HealthCheckType: "invalid", HealthCheckTarget: "node-a"},
		{HealthCheckTimeout: 1, HealthMaxFail: 0, HealthCheckInterval: 1, HealthCheckType: "invalid", HealthCheckTarget: "node-b"},
	}
	hc := NewHealthChecker(context.Background(), healths, conn.NewConn(sideA))
	defer hc.Stop()

	select {
	case payload := <-reports:
		if payload != "node-a=0" {
			t.Fatalf("unexpected health payload: %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for health event")
	}
	if got := hc.checker.Len(); got != 1 {
		t.Fatalf("expected only the valid section to be checked, got %d targets", got)
	}
}

func TestHealthCheckerReportCadence(t *testing.T) {
	sideA, sideB := net.Pipe()
	defer func() { _ = sideB.Close() }()
	reports := readHealth(t, sideB)
	hc := &HealthChecker{serverConn: conn.NewConn(sideA), fails: make(map[healthKey]int)}
	key := healthKey{h: &file.Health{HealthMaxFail: 2}, addr: "a:1"}
	report := func(prev, cur upstream.Health) {
		r := health.Report[healthKey]{Key: key}
		r.Previous, r.Health = prev, cur
		hc.onResult(r)
	}

	report(upstream.HealthUnknown, upstream.HealthUp) // nothing
	report(upstream.HealthUp, upstream.HealthDown)    // 0
	report(upstream.HealthDown, upstream.HealthDown)  // nothing
	report(upstream.HealthDown, upstream.HealthDown)  // 0 again
	report(upstream.HealthDown, upstream.HealthUp)    // 1
	_ = sideA.Close()

	var got []string
	for p := range reports {
		got = append(got, p)
	}
	if strings.Join(got, ",") != "a:1=0,a:1=0,a:1=1" {
		t.Fatalf("unexpected reports %q", got)
	}
}
