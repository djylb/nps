package proxy

import (
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestGlobalClientAndTunnelAdmissionCleanup(t *testing.T) {
	baseline, old := model.GlobalConnections()
	model.SetGlobalMaxConn(int(baseline + 1))
	t.Cleanup(func() { model.SetGlobalMaxConn(int(old)) })
	s := &BaseServer{}
	a := &model.Client{Flow: &model.Flow{}}
	b := &model.Client{Flow: &model.Flow{}, MaxConn: 1}
	release, err := s.AcquireForwarding(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireForwarding(b, nil); err == nil {
		t.Fatal("global limit bypassed")
	}
	if b.CurrentConnections() != 0 {
		t.Fatal("global refusal left child reservation")
	}
	release()
	release() // The returned lease is idempotent.
	task := &model.Tunnel{Client: b, MaxConn: 1}
	release, err = s.AcquireForwarding(b, task.ControlGroup())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireForwarding(b, task.ControlGroup()); err == nil {
		t.Fatal("leaf limit bypassed")
	}
	if task.CurrentConnections() != 1 || b.CurrentConnections() != 1 {
		t.Fatal("ancestor rollback failed")
	}
	release()
	if current, _ := model.GlobalConnections(); current != baseline {
		t.Fatal("lease leaked global slot")
	}
}
