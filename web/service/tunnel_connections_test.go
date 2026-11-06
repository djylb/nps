package service

import "testing"

func TestTunnelConnectionLimitCreateEditClear(t *testing.T) {
	s, r := newRateLimitService()
	if _, err := s.AddTunnel(AddTunnelInput{ClientID: 1, Mode: "tcp", Port: 1234, MaxConn: 5}); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.MaxConn != 5 {
		t.Fatal("create omitted limit")
	}
	if _, err := s.EditTunnel(EditTunnelInput{MaxConn: 2}); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.MaxConn != 2 {
		t.Fatal("edit omitted limit")
	}
	if err := s.ClearTunnel(0, "max_conn"); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.MaxConn != 0 {
		t.Fatal("clear failed")
	}
}
