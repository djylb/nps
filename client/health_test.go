package client

import (
	"context"
	"errors"
	"io"
	logger "log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
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

type flakySink struct {
	mu   sync.Mutex
	fail int
	got  []healthcheck.Report
}

func (s *flakySink) ReportHealth(r healthcheck.Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail > 0 {
		s.fail--
		return errors.New("suspended")
	}
	s.got = append(s.got, r)
	return nil
}

// A report that failed, as during a long suspension, is retried: only the
// end of the session stops the reporter.
func TestHealthReporterSurvivesFailedReport(t *testing.T) {
	old := healthRetryDelay
	healthRetryDelay = time.Millisecond
	defer func() { healthRetryDelay = old }()
	sink := &flakySink{fail: 3}
	r := newHealthReporter(sink)
	defer r.stop()
	r.report("g", "a:1", "0")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		n := len(sink.got)
		sink.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("report never delivered")
}

// The dials of a session's links reach its checks: a failure checks the target
// now, a UDP dial that succeeded says nothing.
func TestCoreHealthObservesDials(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().String()
	h := NewCoreHealth(&flakySink{}).(*coreHealth)
	defer h.Stop()
	h.Set([]healthcheck.Group{{Name: "g", Type: "tcp", Interval: 3600, Timeout: 1, Fall: 3, Targets: []string{addr}}})
	key := h.hc.checker.GroupKeys("g")[0]
	checks := func() int { st, _ := h.hc.checker.Status(key); return int(st.Checks) }
	deadline := time.Now().Add(3 * time.Second)
	for checks() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	before := checks()
	h.ObserveDial("udp", addr, nil)
	h.ObserveDial("tcp", addr, nil)
	time.Sleep(50 * time.Millisecond)
	if checks() != before {
		t.Fatal("a successful dial caused a check")
	}
	h.ObserveDial("tcp", addr, errors.New("refused"))
	for checks() == before && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(5 * time.Millisecond)
	}
	if checks() == before {
		t.Fatal("a failed dial did not check the target")
	}
}
