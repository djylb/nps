package client

import (
	"context"
	"io"
	logger "log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// wantReport waits for one report.
func wantReport(t *testing.T, reports <-chan string, want string, timeout time.Duration) {
	t.Helper()
	select {
	case got := <-reports:
		if got != want {
			t.Fatalf("report %q, want %q", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("no report %q", want)
	}
}

// A self-signed backend is alive: an HTTPS check does not judge its certificate.
func TestHTTPSCheckAcceptsSelfSigned(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	srv.Config.ErrorLog = logger.New(io.Discard, "", 0)
	addr := srv.Listener.Addr().String()
	sideA, sideB := net.Pipe()
	defer func() { _ = sideB.Close() }()
	reports := readHealth(t, sideB)
	hc := NewHealthChecker(context.Background(), legacyHealthSink{signal: conn.NewConn(sideA)})
	defer hc.Stop()
	hc.setGroup(healthcheck.Group{Name: "t1", Type: "https", Path: "/", Interval: 60, Timeout: 3, Fall: 1, Targets: []string{addr}})
	wantReport(t, reports, "t1/"+addr+"=1", 5*time.Second)
}

// Connections succeed while the backend answers 500: traffic must not keep an
// HTTP check from finding that, while a TCP check is postponed by it.
func TestTouchPostponesOnlyTCPChecks(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	sideA, sideB := net.Pipe()
	defer func() { _ = sideB.Close() }()
	reports := readHealth(t, sideB)
	hc := NewHealthChecker(context.Background(), legacyHealthSink{signal: conn.NewConn(sideA)})
	defer hc.Stop()
	hc.setGroup(healthcheck.Group{Name: "h", Type: "http", Path: "/", Interval: 1, Timeout: 1, Fall: 1, Targets: []string{addr}})
	hc.setGroup(healthcheck.Group{Name: "c", Type: "tcp", Interval: 1, Timeout: 1, Fall: 1, Targets: []string{addr}})
	for range 2 {
		select {
		case <-reports:
		case <-time.After(5 * time.Second):
			t.Fatal("no first verdict")
		}
	}
	tcp := hc.checker.GroupKeys("c")[0]
	st, _ := hc.checker.Status(tcp)
	before := st.Checks
	fail.Store(true)
	stop := time.After(1500 * time.Millisecond)
	down := false
	for done := false; !done; {
		hc.touch(addr)
		select {
		case got := <-reports:
			if got != "h/"+addr+"=0" {
				t.Fatalf("report %q", got)
			}
			down = true
		case <-stop:
			done = true
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !down {
		t.Fatal("HTTP check held back by traffic")
	}
	if st, _ := hc.checker.Status(tcp); st.Checks != before {
		t.Fatalf("%d TCP probes during traffic", st.Checks-before)
	}
}

// legacyHealthSink writes reports in a framing these tests read back.
type legacyHealthSink struct{ signal *conn.Conn }

func (s legacyHealthSink) ReportHealth(rep healthcheck.Report) error {
	return legacy.WriteGroupHealth(s.signal, rep)
}
