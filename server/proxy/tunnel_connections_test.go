package proxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestTunnelConnectionLimitRejectsAndReleases(t *testing.T) {
	for _, tc := range []struct {
		name           string
		client, tunnel int
	}{{"tunnel", 10, 1}, {"client", 1, 10}} {
		t.Run(tc.name, func(t *testing.T) {
			task := &file.Tunnel{MaxConn: tc.tunnel, Client: &file.Client{MaxConn: tc.client, Flow: &file.Flow{}}, Flow: &file.Flow{}}
			entered := make(chan struct{}, 3)
			release := make(chan struct{})
			s := NewTunnelModeServer(func(_ *conn.Conn, _ *TunnelModeServer) error { entered <- struct{}{}; <-release; return nil }, &localDialBridge{}, task, false)
			_, first := tcpConnPair(t)
			done := make(chan struct{})
			go func() { s.handleConn(first); close(done) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("first not accepted")
			}
			visitor, second := tcpConnPair(t)
			s.handleConn(second)
			_ = visitor.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := visitor.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("rejected connection: %v", err)
			}
			if task.CurrentConnections() != 1 || task.Client.CurrentConnections() != 1 {
				t.Fatal("rejection leaked a slot")
			}
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("connection not cleaned up")
			}
			if task.CurrentConnections() != 0 || task.Client.CurrentConnections() != 0 {
				t.Fatal("close leaked a slot")
			}
			_, third := tcpConnPair(t)
			s.handleConn(third)
			if task.CurrentConnections() != 0 || task.Client.CurrentConnections() != 0 {
				t.Fatal("replacement leaked a slot")
			}
			select {
			case <-entered:
			default:
				t.Fatal("released slot not reusable")
			}
		})
	}
}

func TestSecretTunnelLimitRejectsBeforeClientReservation(t *testing.T) {
	task := &file.Tunnel{MaxConn: 1, Client: &file.Client{Flow: &file.Flow{}}}
	if !task.GetConn() {
		t.Fatal("initial reservation")
	}
	defer task.CutConn()
	s := NewSecretServer(&localDialBridge{}, task, false, false, false)
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if err := s.HandleSecret(a); err == nil {
		t.Fatal("secret bypassed tunnel limit")
	}
	if task.CurrentConnections() != 1 || task.Client.CurrentConnections() != 1 {
		t.Fatal("secret rejection changed reservations")
	}
}

func TestUDPTunnelRejectionReleasesClientSlot(t *testing.T) {
	for _, flowFailure := range []bool{false, true} {
		task := &file.Tunnel{MaxConn: 1, Client: &file.Client{Flow: &file.Flow{}}, Flow: &file.Flow{}}
		if flowFailure {
			task.Flow.TimeLimit = time.Now().Add(-time.Hour)
		} else if !task.GetConn() {
			t.Fatal("reserve")
		}
		server := NewUdpModeServer(&localDialBridge{}, task, false)
		ctx, cancel := context.WithCancel(context.Background())
		session := &entry{ctx: ctx, cancel: cancel, ch: make(chan packet)}
		server.clientWorker(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}, session)
		wantClient := int32(1)
		if flowFailure {
			wantClient = 0
		}
		if task.Client.CurrentConnections() != wantClient {
			t.Fatal("UDP rejection leaked client slot")
		}
		want := int32(1)
		if flowFailure {
			want = 0
		}
		if task.CurrentConnections() != want {
			t.Fatal("UDP rejection changed tunnel count")
		}
		if !flowFailure {
			task.CutConn()
		}
	}
}

func TestNetxGroupShutdownReleasesHierarchy(t *testing.T) {
	baseline, _ := file.GlobalConnections()
	task := &file.Tunnel{MaxConn: 1, Client: &file.Client{Flow: &file.Flow{}}}
	entered := make(chan struct{}, 1)
	server := NewTunnelModeServer(func(c *conn.Conn, _ *TunnelModeServer) error {
		entered <- struct{}{}
		_, err := c.Read(make([]byte, 1))
		return err
	}, &localDialBridge{}, task, false)
	_, peer := tcpConnPair(t)
	done := make(chan struct{})
	go func() { server.handleConn(peer); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("connection not admitted")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("netx group did not close the active connection")
	}
	if active, _ := file.GlobalConnections(); active != baseline || task.Client.CurrentConnections() != 0 || task.CurrentConnections() != 0 {
		t.Fatal("shutdown leaked ancestor slots")
	}
	_, late := tcpConnPair(t)
	server.handleConn(late)
	select {
	case <-entered:
		t.Fatal("closed group accepted a late connection")
	default:
	}
}
