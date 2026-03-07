package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestNewHealthCheckerInitializesOnlyValidHealthConfigs(t *testing.T) {
	healths := []*file.Health{
		{HealthMaxFail: 1, HealthCheckInterval: 1, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:80"},
		{HealthMaxFail: 0, HealthCheckInterval: 1, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:81"},
	}

	hc := NewHealthChecker(context.Background(), healths, nil)
	t.Cleanup(hc.Stop)

	if got := hc.heap.Len(); got != 1 {
		t.Fatalf("expected only one valid health entry in heap, got %d", got)
	}
	if healths[0].HealthMap == nil {
		t.Fatal("expected valid health entry to initialize HealthMap")
	}
	if healths[0].HealthNextTime.IsZero() {
		t.Fatal("expected valid health entry to have HealthNextTime initialized")
	}
	if healths[1].HealthMap != nil {
		t.Fatal("expected invalid health entry to keep nil HealthMap")
	}
}

func TestDoCheckUnsupportedTypeSendsDownEvent(t *testing.T) {
	sideA, sideB := net.Pipe()
	defer sideA.Close()
	defer sideB.Close()

	readCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		_ = sideB.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, err := sideB.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- string(buf[:n])
	}()

	hc := &HealthChecker{ctx: context.Background(), serverConn: conn.NewConn(sideA)}
	h := &file.Health{HealthCheckTimeout: 1, HealthMaxFail: 1, HealthCheckType: "invalid", HealthCheckTarget: "node-a", HealthMap: map[string]int{}}

	hc.doCheck(h)

	select {
	case err := <-errCh:
		t.Fatalf("expected health event to be written, got error: %v", err)
	case payload := <-readCh:
		if !strings.Contains(payload, "node-a") || !strings.Contains(payload, common.CONN_DATA_SEQ+"0") {
			t.Fatalf("unexpected health payload: %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for health event")
	}
	if got := h.HealthMap["node-a"]; got != 1 {
		t.Fatalf("expected fail count to be incremented to 1, got %d", got)
	}
}

func TestDoCheckTCPSuccessAfterFailuresSendsRecoveryEvent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
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

	sideA, sideB := net.Pipe()
	defer sideA.Close()
	defer sideB.Close()

	readCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		_ = sideB.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, err := sideB.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- string(buf[:n])
	}()

	hc := &HealthChecker{ctx: context.Background(), serverConn: conn.NewConn(sideA)}
	target := ln.Addr().String()
	h := &file.Health{HealthCheckTimeout: 1, HealthMaxFail: 1, HealthCheckType: "tcp", HealthCheckTarget: target, HealthMap: map[string]int{target: 1}}

	hc.doCheck(h)

	select {
	case err := <-errCh:
		t.Fatalf("expected recovery event to be written, got error: %v", err)
	case payload := <-readCh:
		if !strings.Contains(payload, target) || !strings.Contains(payload, common.CONN_DATA_SEQ+"1") {
			t.Fatalf("unexpected recovery payload: %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovery event")
	}
	if got := h.HealthMap[target]; got != 0 {
		t.Fatalf("expected fail count to reset to 0, got %d", got)
	}
}

func TestStopAndDrainHandlesTriggeredTimer(t *testing.T) {
	timer := time.NewTimer(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	stopAndDrain(timer)

	select {
	case <-timer.C:
		t.Fatal("expected timer channel to be drained")
	default:
	}
}

func TestDoCheckHTTPStatusNotOKIncrementsFailCountWithoutEventBeforeThreshold(t *testing.T) {
	server := &httpStatusServer{statusCode: 503}
	ts := server.start(t)
	defer ts.Close()

	hc := &HealthChecker{ctx: context.Background(), client: ts.Client()}
	h := &file.Health{
		HealthCheckTimeout: 1,
		HealthMaxFail:      3,
		HealthCheckType:    "http",
		HealthCheckTarget:  strings.TrimPrefix(ts.URL, "http://"),
		HttpHealthUrl:      "/health",
		HealthMap:          map[string]int{},
	}

	hc.doCheck(h)

	if got := h.HealthMap[h.HealthCheckTarget]; got != 1 {
		t.Fatalf("expected fail count to be 1, got %d", got)
	}
}

type httpStatusServer struct{ statusCode int }

func (s *httpStatusServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(s.statusCode)
		_, _ = io.WriteString(w, "ok")
	}))
}
