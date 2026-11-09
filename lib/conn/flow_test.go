package conn

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"
)

func TestFlowConnStopsAtQuota(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	task := &model.Flow{FlowLimit: 1, InletFlow: 1<<20 - 100}
	client := new(model.Flow)
	c := NewFlowConn(a, task, client)
	defer func() { _ = c.Close() }()
	n, err := c.Write(make([]byte, 1000))
	if n != 101 || err == nil || !strings.Contains(err.Error(), "Task: flow limit exceeded") {
		t.Fatalf("Write past the quota = %d, %v; want 101 and the task's flow limit", n, err)
	}
	if v := task.Values(); v.InletFlow != 1<<20+1 {
		t.Fatalf("task inlet = %d, want a byte past the quota", v.InletFlow)
	}
	if v := client.Values(); v.InletFlow != 101 {
		t.Fatalf("client inlet = %d, want 101", v.InletFlow)
	}
}

func TestFlowConnCountsReadsAsExport(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() { _, _ = b.Write([]byte("hello")) }()
	task := new(model.Flow)
	c := NewFlowConn(a, task, nil)
	defer func() { _ = c.Close() }()
	if _, err := io.ReadFull(c, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	if v := task.Values(); v.ExportFlow != 5 || v.InletFlow != 0 {
		t.Fatalf("Values() = %+v, want 5 bytes out", v)
	}
	if c.RemoteAddr() == nil {
		t.Fatal("no remote address")
	}
}

func TestFlowConnClosedAtExpiry(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	client := &model.Flow{TimeLimit: time.Now().Add(50 * time.Millisecond)}
	c := NewFlowConn(a, nil, client)
	done := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read on an expired flow succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an idle connection outlived its time limit")
	}
	_ = c.Close()
	c2 := NewFlowConn(b, nil, client)
	if _, err := c2.Write([]byte("x")); err == nil || !strings.Contains(err.Error(), "Client: time limit exceeded") {
		t.Fatalf("Write on an expired flow = %v", err)
	}
}
