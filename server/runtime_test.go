package server

import (
	"testing"
	"time"
)

func TestGetDashboardDataReturnsDetachedSnapshot(t *testing.T) {
	cacheMu.Lock()
	oldCache := dashboardCache
	oldLastRefresh := lastRefresh
	oldLastFullRefresh := lastFullRefresh
	dashboardCache = map[string]interface{}{
		"count": 1,
		"nested": map[string]interface{}{
			"value": 2,
		},
	}
	lastRefresh = time.Now()
	lastFullRefresh = time.Now()
	cacheMu.Unlock()
	defer func() {
		cacheMu.Lock()
		dashboardCache = oldCache
		lastRefresh = oldLastRefresh
		lastFullRefresh = oldLastFullRefresh
		cacheMu.Unlock()
	}()

	snapshot := GetDashboardData(false)
	snapshot["count"] = 99

	nested, ok := snapshot["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("snapshot nested type = %T, want map[string]interface{}", snapshot["nested"])
	}
	nested["value"] = 77

	again := GetDashboardData(false)
	if again["count"] == 99 {
		t.Fatalf("GetDashboardData() returned shared top-level cache: %+v", again)
	}
	againNested, ok := again["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("again nested type = %T, want map[string]interface{}", again["nested"])
	}
	if againNested["value"] == 77 {
		t.Fatalf("GetDashboardData() returned shared nested cache: %+v", againNested)
	}
}
