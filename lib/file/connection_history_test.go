package file

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/servercfg"
)

func TestConnectionHistoryPairsAndRestart(t *testing.T) {
	h := new(ConnectionHistory)
	now := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	for i := 0; i < 24; i++ {
		h.record(true, now.Add(time.Duration(i)*time.Minute))
		h.record(true, now)
		h.record(false, now.Add(time.Duration(i)*time.Minute+time.Second))
		h.record(false, now)
	}
	if len(h.events) != 5 || h.events[0].OnlineTime != "2026-10-20 01:21:03" || h.events[0].OfflineTime != "2026-10-20 01:21:04" {
		t.Fatalf("unexpected history: %+v", h.events)
	}
	h.record(true, now.Add(time.Hour))
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var restored ConnectionHistory
	if err = json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	restored.record(false, now.Add(2*time.Hour))
	restored.record(true, now.Add(3*time.Hour))
	if len(restored.events) != 5 || restored.events[3].OfflineTime != "" || restored.events[4].OnlineTime != "2026-10-20 04:02:03" {
		t.Fatalf("restart changed incomplete pair: %+v", restored.events)
	}
	restored.record(false, now.Add(4*time.Hour))
	if restored.events[4].OfflineTime != "2026-10-20 05:02:03" {
		t.Fatal("new session not closed")
	}
}

func TestConnectionHistoryConcurrentSnapshot(t *testing.T) {
	h := new(ConnectionHistory)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for n := 0; n < 100; n++ {
				h.record(n%2 == 0, time.Now())
				if _, err := json.Marshal(h); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if len(h.events) > 5 {
		t.Fatal("unbounded history")
	}
}

func TestConnectionHistoryConfiguredLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nps.conf")
	set := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("client_history_limit="+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := servercfg.Load(path); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { set("5") })
	set("2")
	h := new(ConnectionHistory)
	for i := 0; i < 6; i++ {
		h.record(true, time.Now())
		h.record(false, time.Now())
	}
	if len(h.events) != 2 {
		t.Fatal("configured cap ignored")
	}
	set("0")
	h.record(true, time.Now())
	if len(h.events) != 0 {
		t.Fatal("disabled history retained records")
	}
	set("-1")
	if connectionHistoryLimit() != 5 {
		t.Fatal("negative limit did not fall back")
	}
}
