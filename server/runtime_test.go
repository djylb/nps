package server

import (
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestGetDashboardDataReturnsDetachedSnapshot(t *testing.T) {
	s := New(file.New(t.TempDir()), Config{CoreEntry: stubEntry{}})
	t.Cleanup(func() { _ = s.Close() })
	d := &s.dash
	d.cacheMu.Lock()
	d.cache = map[string]any{
		"count": 1,
		"nested": map[string]any{
			"value": 2,
		},
	}
	d.lastRefresh = time.Now()
	d.lastFullRefresh = time.Now()
	d.cacheMu.Unlock()

	snapshot := s.DashboardData(false)
	snapshot["count"] = 99

	nested, ok := snapshot["nested"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot nested type = %T, want map[string]interface{}", snapshot["nested"])
	}
	nested["value"] = 77

	again := s.DashboardData(false)
	if again["count"] == 99 {
		t.Fatalf("DashboardData() returned shared top-level cache: %+v", again)
	}
	againNested, ok := again["nested"].(map[string]any)
	if !ok {
		t.Fatalf("again nested type = %T, want map[string]interface{}", again["nested"])
	}
	if againNested["value"] == 77 {
		t.Fatalf("DashboardData() returned shared nested cache: %+v", againNested)
	}
}
