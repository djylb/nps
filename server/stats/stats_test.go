package stats

import "testing"

func resetStatusState() {
	ssMu.Lock()
	defer ssMu.Unlock()
	for i := range statBuf {
		statBuf[i] = nil
	}
	statIdx = 0
	statFilled = false
}

func fillStatusEntries(n int) {
	for i := range n {
		statBuf[i] = map[string]any{"id": i}
	}
	statIdx = n
	if n >= statusCap {
		statIdx = n % statusCap
		statFilled = true
	}
}

func TestStatusCountAndSnapshotWithoutWrap(t *testing.T) {
	resetStatusState()
	fillStatusEntries(3)

	if got := statusCount(); got != 3 {
		t.Fatalf("statusCount()=%d, want 3", got)
	}

	snapshot := StatusSnapshot()
	if len(snapshot) != 3 {
		t.Fatalf("len(StatusSnapshot())=%d, want 3", len(snapshot))
	}

	for i := range 3 {
		if snapshot[i]["id"].(int) != i {
			t.Fatalf("snapshot[%d].id=%v, want %d", i, snapshot[i]["id"], i)
		}
	}
}

func TestStatusSnapshotWithWrap(t *testing.T) {
	resetStatusState()
	ssMu.Lock()
	for i := range statusCap {
		statBuf[i] = map[string]any{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	snapshot := StatusSnapshot()
	if len(snapshot) != statusCap {
		t.Fatalf("len(StatusSnapshot())=%d, want %d", len(snapshot), statusCap)
	}
	if snapshot[0]["id"].(int) != 100 {
		t.Fatalf("snapshot[0].id=%v, want 100", snapshot[0]["id"])
	}
	if snapshot[len(snapshot)-1]["id"].(int) != 99 {
		t.Fatalf("snapshot[last].id=%v, want 99", snapshot[len(snapshot)-1]["id"])
	}
}

func TestChartDecilesEdgeCasesAndSampling(t *testing.T) {
	resetStatusState()
	if got := ChartDeciles(); got != nil {
		t.Fatalf("ChartDeciles()=%v, want nil for empty state", got)
	}

	resetStatusState()
	fillStatusEntries(5)
	small := ChartDeciles()
	if len(small) != 5 {
		t.Fatalf("len(ChartDeciles())=%d, want 5", len(small))
	}
	for i := range 5 {
		if small[i]["id"].(int) != i {
			t.Fatalf("small[%d].id=%v, want %d", i, small[i]["id"], i)
		}
	}

	resetStatusState()
	ssMu.Lock()
	for i := range statusCap {
		statBuf[i] = map[string]any{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	deciles := ChartDeciles()
	if len(deciles) != 10 {
		t.Fatalf("len(ChartDeciles())=%d, want 10", len(deciles))
	}

	for i := range 10 {
		pos := (i * (statusCap - 1)) / 9
		expected := (100 + pos) % statusCap
		if deciles[i]["id"].(int) != expected {
			t.Fatalf("deciles[%d].id=%v, want %d", i, deciles[i]["id"], expected)
		}
	}
}
