package controllers

import (
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

func TestRemoveRepeatedElement(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []string{}, want: nil},
		{name: "unique", in: []string{"1.1.1.1", "2.2.2.2"}, want: []string{"1.1.1.1", "2.2.2.2"}},
		{name: "duplicate keep last occurrence order", in: []string{"a", "b", "a", "c", "b"}, want: []string{"a", "c", "b"}},
		{name: "keep blank line once", in: []string{"", "", "10.0.0.1"}, want: []string{"", "10.0.0.1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveRepeatedElement(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RemoveRepeatedElement(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClearClientStatusBasicFields(t *testing.T) {
	timeLimit := time.Now().Add(1 * time.Hour)

	tests := []struct {
		name   string
		mode   string
		assert func(t *testing.T, c *file.Client)
	}{
		{
			name: "clear flow limit",
			mode: "flow_limit",
			assert: func(t *testing.T, c *file.Client) {
				if c.Flow.FlowLimit != 0 {
					t.Fatalf("expected FlowLimit to be 0, got %d", c.Flow.FlowLimit)
				}
			},
		},
		{
			name: "clear time limit",
			mode: "time_limit",
			assert: func(t *testing.T, c *file.Client) {
				if !c.Flow.TimeLimit.IsZero() {
					t.Fatalf("expected TimeLimit to be zero, got %v", c.Flow.TimeLimit)
				}
			},
		},
		{
			name: "clear connection limit",
			mode: "conn_limit",
			assert: func(t *testing.T, c *file.Client) {
				if c.MaxConn != 0 {
					t.Fatalf("expected MaxConn to be 0, got %d", c.MaxConn)
				}
			},
		},
		{
			name: "clear tunnel limit",
			mode: "tunnel_limit",
			assert: func(t *testing.T, c *file.Client) {
				if c.MaxTunnelNum != 0 {
					t.Fatalf("expected MaxTunnelNum to be 0, got %d", c.MaxTunnelNum)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &file.Client{
				Flow:         &file.Flow{FlowLimit: 1024, TimeLimit: timeLimit},
				Rate:         rate.NewRate(1024),
				MaxConn:      10,
				MaxTunnelNum: 20,
			}

			clearClientStatus(c, tt.mode)
			tt.assert(t, c)
		})
	}
}

func TestClearClientStatusRateLimit(t *testing.T) {
	t.Run("create rate when nil", func(t *testing.T) {
		c := &file.Client{Flow: &file.Flow{}, RateLimit: 123, Rate: nil}

		clearClientStatus(c, "rate_limit")

		if c.RateLimit != 0 {
			t.Fatalf("expected RateLimit to be 0, got %d", c.RateLimit)
		}
		if c.Rate == nil {
			t.Fatal("expected Rate to be initialized")
		}
		if c.Rate.Limit() != 0 {
			t.Fatalf("expected limiter limit to be 0, got %d", c.Rate.Limit())
		}
	})

	t.Run("reset existing rate limit", func(t *testing.T) {
		c := &file.Client{Flow: &file.Flow{}, RateLimit: 256, Rate: rate.NewRate(256 * 1024)}

		clearClientStatus(c, "rate_limit")

		if c.RateLimit != 0 {
			t.Fatalf("expected RateLimit to be 0, got %d", c.RateLimit)
		}
		if c.Rate.Limit() != 0 {
			t.Fatalf("expected limiter limit to be reset to 0, got %d", c.Rate.Limit())
		}
	})
}
