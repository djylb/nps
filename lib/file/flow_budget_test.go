package file

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"
)

func TestFlowBudgetTakesOverPersistedTraffic(t *testing.T) {
	flow := &model.Flow{FlowLimit: 1, InletFlow: 1 << 20}
	if traffic, _ := flow.Over(); traffic {
		t.Fatal("a flow exactly at its quota reported over")
	}
	if got := flow.Budget().Remaining(); got != 1 {
		t.Fatalf("Remaining() = %d, want 1: the persisted traffic not taken over", got)
	}
	flow.Add(1, 0)
	if traffic, _ := flow.Over(); !traffic {
		t.Fatal("a flow past its quota not reported over")
	}
	flow.ResetTraffic()
	flow.Add(3, 5)
	if traffic, expired := flow.Over(); traffic || expired {
		t.Fatal("reset did not reset the budget")
	}
	if v := flow.Values(); v.InletFlow != 3 || v.ExportFlow != 5 || v.Used != 8 {
		t.Fatalf("Values() = %+v after the reset, want 3 in, 5 out", v)
	}
	flow.SetTimeLimit(time.Now().Add(-time.Second))
	if _, expired := flow.Over(); !expired {
		t.Fatal("time limit ignored")
	}
	if flow.Budget().Remaining() != 0 {
		t.Fatal("budget not expired")
	}
	flow.SetTimeLimit(time.Time{})
	if _, expired := flow.Over(); expired {
		t.Fatal("clearing the time limit did not reopen the flow")
	}
	flow.SetLimits(0, time.Time{})
	flow.Add(2<<20, 0)
	if traffic, _ := flow.Over(); traffic {
		t.Fatal("clearing the quota left the stop")
	}
}

func TestFlowCounterPersistenceDuringTransfer(t *testing.T) {
	f := new(model.Flow)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			f.Add(3, 5)
		}
	})
	for range 200 {
		if _, err := json.Marshal(f); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	v := f.Values()
	if v.InletFlow != 600 || v.ExportFlow != 1000 {
		t.Fatal("persistent adapter lost counter bytes")
	}
	var back model.Flow
	b, _ := json.Marshal(f)
	if err := json.Unmarshal(b, &back); err != nil || back.Values().InletFlow != 600 {
		t.Fatalf("JSON round trip = %s, %v", b, err)
	}
}
