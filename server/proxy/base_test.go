package proxy

import (
	"testing"
	"time"

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
