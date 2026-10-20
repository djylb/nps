package proxy

import (
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/byterate"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestAcquireForwarding(t *testing.T) {
	s := &BaseServer{}

	t.Run("expired service", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{TimeLimit: time.Now().Add(-time.Second)}}
		_, err := s.AcquireForwarding(client, nil)
		if err == nil || err.Error() != "service access expired" {
			t.Fatalf("AcquireForwarding() error = %v, want service access expired", err)
		}
	})

	t.Run("traffic limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 1}, MaxConn: 10}
		_, err := s.AcquireForwarding(client, nil)
		if err == nil || err.Error() != "traffic limit exceeded" {
			t.Fatalf("AcquireForwarding() error = %v, want traffic limit exceeded", err)
		}
	})

	t.Run("connection limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 1}
		if !client.GetConn() {
			t.Fatal("seed reservation")
		}
		defer client.CutConn()
		_, err := s.AcquireForwarding(client, nil)
		if err == nil || !errors.Is(err, byterate.ErrTooManyConns) {
			t.Fatalf("AcquireForwarding() error = %v, want connection limit exceeded", err)
		}
	})

	t.Run("success increments connection count", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 2, NowConn: 0}
		release, err := s.AcquireForwarding(client, nil)
		if err != nil {
			t.Fatalf("AcquireForwarding() unexpected error = %v", err)
		}
		defer release()
		if client.CurrentConnections() != 1 {
			t.Fatalf("client.CurrentConnections() = %d, want 1", client.CurrentConnections())
		}
	})
}

func TestFlowAddAndFlowAddHost(t *testing.T) {
	taskFlow := &file.Flow{}
	hostFlow := &file.Flow{}
	s := &BaseServer{Task: &file.Tunnel{Flow: taskFlow}}
	h := &file.Host{Flow: hostFlow}

	s.FlowAdd(10, 20)
	s.FlowAddHost(h, 30, 40)

	if taskFlow.InletFlow != 10 || taskFlow.ExportFlow != 20 {
		t.Fatalf("task flow mismatch: inlet=%d export=%d", taskFlow.InletFlow, taskFlow.ExportFlow)
	}
	if hostFlow.InletFlow != 30 || hostFlow.ExportFlow != 40 {
		t.Fatalf("host flow mismatch: inlet=%d export=%d", hostFlow.InletFlow, hostFlow.ExportFlow)
	}
}

func TestAuth(t *testing.T) {
	t.Run("auth success", func(t *testing.T) {
		s := &BaseServer{}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))

		if err := s.Auth(r, nil, "user", "pass", nil, nil); err != nil {
			t.Fatalf("Auth() unexpected error = %v", err)
		}
	})

	t.Run("auth failure writes unauthorized bytes and closes conn", func(t *testing.T) {
		s := &BaseServer{}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("bad:creds")))

		serverSide, clientSide := net.Pipe()
		defer func() { _ = clientSide.Close() }()
		c := conn.NewConn(serverSide)
		errCh := make(chan error, 1)
		go func() {
			errCh <- s.Auth(r, c, "user", "pass", nil, nil)
		}()

		buf := make([]byte, len(common.UnauthorizedBytes))
		if _, err := io.ReadFull(clientSide, buf); err != nil {
			t.Fatalf("Read() unauthorized bytes error = %v", err)
		}
		if got := string(buf); got != common.UnauthorizedBytes {
			t.Fatalf("unauthorized bytes = %q, want %q", got, common.UnauthorizedBytes)
		}

		err := <-errCh
		if err == nil || err.Error() != "401 Unauthorized" {
			t.Fatalf("Auth() error = %v, want 401 Unauthorized", err)
		}
	})
}

func TestWriteConnFail(t *testing.T) {
	s := &BaseServer{ErrorContent: []byte("detail")}
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	go s.writeConnFail(serverSide)

	buf := make([]byte, len(common.ConnectionFailBytes)+len(s.ErrorContent))
	if _, err := io.ReadFull(clientSide, buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := common.ConnectionFailBytes + "detail"
	if got := string(buf); got != want {
		t.Fatalf("writeConnFail() bytes = %q, want %q", got, want)
	}

	_ = serverSide.Close()
}

func TestAcquireForwardingAtTrafficLimitBoundary(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{Flow: &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 0}, MaxConn: 2}

	release, err := s.AcquireForwarding(client, nil)
	if err != nil {
		t.Fatalf("AcquireForwarding() unexpected error = %v", err)
	}
	defer release()
	if client.CurrentConnections() != 1 {
		t.Fatalf("client.CurrentConnections() = %d, want 1", client.CurrentConnections())
	}
}

func TestAuthWithMultiAccount(t *testing.T) {
	s := &BaseServer{}
	r := httptest.NewRequest("GET", "http://example.com", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("worker:p@ss")))
	multi := &file.MultiAccount{AccountMap: map[string]string{"worker": "p@ss"}}

	if err := s.Auth(r, nil, "", "", multi, nil); err != nil {
		t.Fatalf("Auth() with multi-account unexpected error = %v", err)
	}
}

func TestWriteConnFailWithNilErrorContent(t *testing.T) {
	s := &BaseServer{}
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	go s.writeConnFail(serverSide)

	buf := make([]byte, len(common.ConnectionFailBytes))
	if _, err := io.ReadFull(clientSide, buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf); !strings.HasPrefix(got, common.ConnectionFailBytes) {
		t.Fatalf("writeConnFail() bytes = %q, want prefix %q", got, common.ConnectionFailBytes)
	}

	_ = serverSide.Close()
}

func TestTunnelModeServerCloseBeforeStartReleasesPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	task := &file.Tunnel{Mode: "tcp", ServerIp: "127.0.0.1", Port: port, Client: &file.Client{Cnf: &file.Config{}, Flow: &file.Flow{}}, Flow: &file.Flow{}, Target: &file.Target{}}

	// Bound but never started: Close must release the port and Start must not reopen it.
	bound := NewTunnelModeServer(ProcessTunnel, failBridge{}, task, false)
	if err := bound.Bind(); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if err := bound.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bound.Start(); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Start() after Close() = %v, want ErrServerClosed", err)
	}

	// Closed before Bind: neither Bind nor Start may open the port.
	closed := NewTunnelModeServer(ProcessTunnel, failBridge{}, task, false)
	_ = closed.Close()
	if err := closed.Bind(); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Bind() after Close() = %v, want ErrServerClosed", err)
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %d still in use after Close(): %v", port, err)
	}
	_ = l.Close()
}
