package proxy

import (
	"encoding/base64"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestIn(t *testing.T) {
	tests := []struct {
		name   string
		target string
		list   []string
		want   bool
	}{
		{name: "finds existing element", target: "b", list: []string{"c", "a", "b"}, want: true},
		{name: "returns false for missing element", target: "d", list: []string{"c", "a", "b"}, want: false},
		{name: "handles empty slice", target: "a", list: []string{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := in(tt.target, tt.list); got != tt.want {
				t.Fatalf("in(%q, %v) = %v, want %v", tt.target, tt.list, got, tt.want)
			}
		})
	}
}

func TestCheckFlowAndConnNum(t *testing.T) {
	s := &BaseServer{}

	t.Run("expired service", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{TimeLimit: time.Now().Add(-time.Second)}}
		err := s.CheckFlowAndConnNum(client)
		if err == nil || err.Error() != "service access expired" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want service access expired", err)
		}
	})

	t.Run("traffic limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 1}, MaxConn: 10}
		err := s.CheckFlowAndConnNum(client)
		if err == nil || err.Error() != "traffic limit exceeded" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want traffic limit exceeded", err)
		}
	})

	t.Run("connection limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 1, NowConn: 1}
		err := s.CheckFlowAndConnNum(client)
		if err == nil || err.Error() != "connection limit exceeded" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want connection limit exceeded", err)
		}
	})

	t.Run("success increments connection count", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 2, NowConn: 0}
		err := s.CheckFlowAndConnNum(client)
		if err != nil {
			t.Fatalf("CheckFlowAndConnNum() unexpected error = %v", err)
		}
		if client.NowConn != 1 {
			t.Fatalf("client.NowConn = %d, want 1", client.NowConn)
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

func TestCheckFlowAndConnNumAtTrafficLimitBoundary(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{Flow: &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 0}, MaxConn: 2}

	err := s.CheckFlowAndConnNum(client)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() unexpected error = %v", err)
	}
	if client.NowConn != 1 {
		t.Fatalf("client.NowConn = %d, want 1", client.NowConn)
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
