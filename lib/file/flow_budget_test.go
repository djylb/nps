package file

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestFlowLibraryCounterBudgetRestorationAndReset(t *testing.T) {
	flow := &Flow{FlowLimit: 1, InletFlow: 1 << 20}
	if traffic, _ := flow.Over(); traffic {
		t.Fatal("legacy exact quota boundary changed")
	}
	if traffic, _ := flow.AddOver(1, 0); !traffic {
		t.Fatal("budget ignored restored payload usage")
	}
	if flow.counter == nil || flow.trafficBudget.Used() != (1<<20)+1 {
		t.Fatal("library controls not fed the persisted usage")
	}
	flow.ResetTraffic()
	if traffic, expired := flow.AddOver(3, 5); traffic || expired {
		t.Fatal("reset did not reset budget usage")
	}
	if flow.InletFlow != 3 || flow.ExportFlow != 5 {
		t.Fatal("counter restore counted the reset as new traffic")
	}
	flow.SetTimeLimit(time.Now().Add(-time.Second))
	if _, expired := flow.Over(); !expired {
		t.Fatal("budget expiry ignored")
	}
	flow.SetTimeLimit(time.Time{})
	if _, expired := flow.Over(); expired {
		t.Fatal("clearing expiry did not reopen budget")
	}
	flow.SetLimits(0, time.Time{})
	if traffic, _ := flow.AddOver(2<<20, 0); traffic {
		t.Fatal("clearing quota left stale stop step")
	}
}

func TestFlowCounterPersistenceDuringTransfer(t *testing.T) {
	f := new(Flow)
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
}
