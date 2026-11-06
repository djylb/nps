package file

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTunnelConnectionLimitConcurrent(t *testing.T) {
	task := &Tunnel{MaxConn: 7}
	var wg sync.WaitGroup
	var granted atomic.Int32
	start := make(chan struct{})
	for range 100 {
		wg.Go(func() {
			<-start
			if task.GetConn() {
				granted.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if granted.Load() != 7 || task.CurrentConnections() != 7 {
		t.Fatalf("granted %d, count %d", granted.Load(), task.CurrentConnections())
	}
	for range 7 {
		task.CutConn()
	}
	if task.CurrentConnections() != 0 {
		t.Fatal("cleanup underflow")
	}
	task.SetMaxConn(-1)
	for range 20 {
		if !task.GetConn() {
			t.Fatal("zero limit is not unlimited")
		}
	}
	for range 20 {
		task.CutConn()
	}
}

func TestTunnelConnectionLimitAcrossEdit(t *testing.T) {
	task := &Tunnel{MaxConn: 3}
	db := newTestDb(t)
	if err := db.NewTask(task); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if !task.GetConn() {
			t.Fatal("initial reservation")
		}
	}
	clone := task.Clone()
	clone.SetMaxConn(1)
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	if clone.GetConn() {
		t.Fatal("edit forgot existing connections")
	}
	for range 2 {
		task.CutConn()
	}
	if clone.GetConn() {
		t.Fatal("lowering limit admitted while at capacity")
	}
	task.CutConn()
	if !clone.GetConn() {
		t.Fatal("old connection did not release shared slot")
	}
	if task.CurrentConnections() != 1 {
		t.Fatal("edited tunnel has independent count")
	}
	clone.CutConn()
	data, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Tunnel
	if err = json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.MaxConn != 1 || loaded.runtimeRate != nil {
		t.Fatal("configuration/runtime persistence mismatch")
	}
}
