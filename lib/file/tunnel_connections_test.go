package file

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestTunnelConnectionLimitConcurrent(t *testing.T) {
	task := &model.Tunnel{MaxConn: 7}
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
